// RadioArchive's recorder plugin (SPEC.md R1, ADR 0001, ADR 0002): a thin
// trunk-recorder plugin that hands each call's PCM, its identity, its
// transmissions and its call_end to the recorder client over UDP on
// localhost. It replaces simplestream. It does no authentication, encoding
// or buffering: every message is one non-blocking sendto, and a failed send
// is counted and forgotten.
//
// v1 supports P25 trunked systems only (SPEC.md 6.1). Other systems are
// logged once at startup and ignored.
//
// With "handoffDir" set (ADR 0045), call_end hard-links each call's WAV into
// that directory and names the link in call_end instead, so a recorder shared
// with another uploader can keep "audioArchive": false.
//
// Built against trunk-recorder v5.2.1 (recorder/plugin/Dockerfile pins it).
// trunk-recorder references below are to that release.

#include <plugin_manager/plugin_api.h>
#include <recorders/recorder.h>
#include <state.h>

#include <boost/dll/alias.hpp>
#include <boost/log/trivial.hpp>

#include <arpa/inet.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <unistd.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <map>
#include <mutex>
#include <random>
#include <set>
#include <string>
#include <utility>
#include <vector>

#include "wire.h"

#ifndef RADIOARCHIVE_PLUGIN_VERSION
#define RADIOARCHIVE_PLUGIN_VERSION "0.1.0"
#endif

// formatter.h declares this global and Plugin_Api::init (inline in plugin_api.h)
// sets it. trunk-recorder defines it in its static library, which isn't
// exported to plugins (its own plugins each link a copy of that library),
// so the plugin has its own. Nothing in the plugin reads it.
int frequency_format = 0;

namespace {

using radioarchive::wire::AudioHeader;
namespace wire = radioarchive::wire;

constexpr const char *kLog = "[radioarchive] ";
constexpr uint16_t kDefaultPort = 9850;
constexpr long kP25Rate = 8000;
// A call the plugin has state for but never sees end (trunk-recorder can
// conclude a call without call_end, for example one it didn't record) is
// forgotten after this long.
constexpr int64_t kForgetCallMs = 2 * 60 * 60 * 1000;

int64_t now_ms() {
  return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::system_clock::now().time_since_epoch()).count();
}

// patched_talkgroups (ADR 0002): System::get_talkgroup_patch with the call's
// own talkgroup removed, [] when there's no patch.
std::vector<long> others(const std::vector<unsigned long> &patch, long talkgroup) {
  std::vector<long> out;
  for (unsigned long tg : patch) {
    if (long(tg) != talkgroup && std::find(out.begin(), out.end(), long(tg)) == out.end()) out.push_back(long(tg));
  }
  return out;
}

int8_t slot_of(bool phase2_tdma, int slot) {
  return phase2_tdma ? int8_t(slot) : int8_t(-1);
}

uint32_t hz(double freq) {
  return freq <= 0 ? 0 : uint32_t(std::llround(freq));
}

std::string base_name(const std::string &path) {
  const size_t slash = path.find_last_of('/');
  return slash == std::string::npos ? path : path.substr(slash + 1);
}

// Whether two paths are on different mounts, which link(2) refuses with
// EXDEV even within one filesystem (two bind mounts of it, say). False when
// it can't tell.
bool different_mounts(const std::string &a, const std::string &b) {
#ifdef STATX_MNT_ID
  struct statx sa {}, sb {};
  if (statx(AT_FDCWD, a.c_str(), 0, STATX_MNT_ID, &sa) != 0 || statx(AT_FDCWD, b.c_str(), 0, STATX_MNT_ID, &sb) != 0) return false;
  if (!(sa.stx_mask & STATX_MNT_ID) || !(sb.stx_mask & STATX_MNT_ID)) return false;
  return sa.stx_mnt_id != sb.stx_mnt_id;
#else
  return false;
#endif
}

struct SystemInfo {
  std::string short_name;
  std::string type;
  bool supported = false;
};

// What the plugin remembers about one call, keyed by (sys_num, call_num).
struct CallState {
  int64_t start_time = 0;  // Call::get_start_time(), the identity's start_time
  uint32_t last_unit = 0;  // the unit in the last transmission sent, 0 if none
  std::vector<long> patched;  // from the last call_start, read on the main thread
  int64_t touched_ms = 0;
};

class RadioArchivePlugin : public Plugin_Api {
 public:
  RadioArchivePlugin() {
    std::random_device rd;
    boot_id_ = (uint32_t(rd()) << 16) ^ uint32_t(rd());
  }

  ~RadioArchivePlugin() override {
    if (fd_ >= 0) close(fd_);
  }

  int parse_config(json config_data) override {
    long port = config_data.value("port", long(kDefaultPort));
    if (port < 1 || port > 65535) {
      BOOST_LOG_TRIVIAL(error) << kLog << "port " << port << " is out of range; using " << kDefaultPort;
      port = kDefaultPort;
    }
    port_ = uint16_t(port);

    std::string dir;
    if (config_data.contains("handoffDir") && config_data["handoffDir"].is_string()) {
      dir = config_data["handoffDir"].get<std::string>();
    } else if (config_data.contains("handoffDir") && !config_data["handoffDir"].is_null()) {
      BOOST_LOG_TRIVIAL(error) << kLog << "handoffDir must be a string; ignored";
    }
    while (dir.size() > 1 && dir.back() == '/') dir.pop_back();
    if (!dir.empty() && dir.front() != '/') {
      BOOST_LOG_TRIVIAL(error) << kLog << "handoffDir \"" << dir << "\" isn't an absolute path; ignored, so call_end names trunk-recorder's own files";
      dir.clear();
    }
    handoff_dir_ = dir;
    return 0;
  }

  int init(Config *config, std::vector<Source *> sources, std::vector<System *> systems) override {
    Plugin_Api::init(config, sources, systems);
    if (config) capture_dir_ = config->capture_dir;
    if (config && !config->enable_audio_streaming) {
      BOOST_LOG_TRIVIAL(error) << kLog << "audioStreaming is off in trunk-recorder's config, so no live audio will reach the recorder client";
    }
    learn_systems(systems);
    return 0;
  }

  int setup_systems(std::vector<System *> systems) override {
    learn_systems(systems);
    return 0;
  }

  int start() override {
    fd_ = socket(AF_INET, SOCK_DGRAM | SOCK_NONBLOCK | SOCK_CLOEXEC, 0);
    if (fd_ < 0) {
      BOOST_LOG_TRIVIAL(error) << kLog << "can't open a UDP socket: " << std::strerror(errno);
      return 0;  // trunk-recorder keeps recording; the plugin just sends nothing
    }
    int sndbuf = 1 << 20;
    setsockopt(fd_, SOL_SOCKET, SO_SNDBUF, &sndbuf, sizeof(sndbuf));

    sockaddr_in local{};
    local.sin_family = AF_INET;
    local.sin_addr.s_addr = htonl(INADDR_LOOPBACK);  // never a non-loopback address
    local.sin_port = 0;
    if (bind(fd_, reinterpret_cast<sockaddr *>(&local), sizeof(local)) != 0) {
      BOOST_LOG_TRIVIAL(error) << kLog << "can't bind 127.0.0.1: " << std::strerror(errno);
    }
    dest_.sin_family = AF_INET;
    dest_.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    dest_.sin_port = htons(port_);
    BOOST_LOG_TRIVIAL(info) << kLog << "plugin " << RADIOARCHIVE_PLUGIN_VERSION << " sending to 127.0.0.1:" << port_ << ", boot_id " << boot_id_;
    check_handoff_dir();
    return 0;
  }

  int stop() override {
    if (fd_ >= 0) close(fd_);
    fd_ = -1;
    return 0;
  }

  // Runs on trunk-recorder's main thread, again for the same Call when the
  // unit talking changes (monitor_systems.cc:479-485), and also for calls it
  // isn't recording (:502-556), so only a RECORDING call counts.
  int call_start(Call *call) override {
    const SystemInfo *sys = system_of(call->get_sys_num());
    if (!sys || !sys->supported || call->get_state() != RECORDING) return 0;

    const long talkgroup = call->get_talkgroup();
    std::vector<long> patched;
    if (System *s = call->get_system()) patched = others(s->get_talkgroup_patch(talkgroup), talkgroup);
    const long src = call->get_current_source_id();

    std::string tx;
    {
      std::lock_guard<std::mutex> lock(mu_);
      CallState &st = state_for(call);
      st.patched = patched;
      if (src > 0 && uint32_t(src) != st.last_unit) {
        st.last_unit = uint32_t(src);
        tx = transmission_json(call, st, *sys);
      }
    }
    if (!tx.empty()) send(wire::kTypeTransmission, tx);
    return 0;
  }

  // Runs on each recorder's GNU Radio thread (gr_blocks/plugin_wrapper_impl.cc),
  // so several calls arrive at once.
  int audio_stream(Call *call, Recorder *recorder, int16_t *samples, int sampleCount) override {
    if (sampleCount <= 0 || samples == nullptr) return 0;
    const int sys_num = call->get_sys_num();
    const SystemInfo *sys = system_of(sys_num);
    if (!sys || !sys->supported) return 0;

    const long rate = recorder ? recorder->get_wav_hz() : kP25Rate;
    if (rate != kP25Rate) {
      std::lock_guard<std::mutex> lock(mu_);
      if (wrong_rate_logged_.insert(sys_num).second) {
        BOOST_LOG_TRIVIAL(error) << kLog << "[" << sys->short_name << "] audio at " << rate << " Hz, not P25's " << kP25Rate << " Hz; not sent";
      }
      return 0;
    }

    AudioHeader h;
    h.call_num = call->get_call_num();
    h.talkgroup = int32_t(call->get_talkgroup());
    h.freq = hz(call->get_freq());
    h.sys_num = uint16_t(sys_num);
    h.sample_rate = uint16_t(rate);
    h.tdma_slot = slot_of(call->get_phase2_tdma(), call->get_tdma_slot());
    h.short_name = sys->short_name;
    const long src = call->get_current_source_id();

    std::string tx;
    {
      std::lock_guard<std::mutex> lock(mu_);
      CallState &st = state_for(call);
      h.start_time = st.start_time;
      if (src > 0 && uint32_t(src) != st.last_unit) {
        st.last_unit = uint32_t(src);
        tx = transmission_json(call, st, *sys);
      }
      // Before a unit is known, the last one sent (simplestream falls back to
      // the last transmission's source the same way).
      h.unit = src > 0 ? uint32_t(src) : st.last_unit;
    }
    if (!tx.empty()) send(wire::kTypeTransmission, tx);

    for (int off = 0; off < sampleCount; off += int(wire::kMaxAudioSamples)) {
      const size_t n = std::min<size_t>(wire::kMaxAudioSamples, size_t(sampleCount - off));
      send_datagram(wire::audio(boot_id_, next_seq(), h, samples + off, n));
    }
    return 0;
  }

  // Runs in a call concluder worker once trunk-recorder has written the WAV
  // (call_concluder.cc:900-959). A non-zero return makes trunk-recorder retry,
  // so this always returns 0. Other plugins' call_end run in the same thread,
  // so it never blocks: the hand-off is one link(2).
  int call_end(Call_Data_t info) override {
    const SystemInfo *sys = system_of(info.sys_num);
    if (!sys || !sys->supported) return 0;

    // The identity's start_time is Call::get_start_time(), as in every audio
    // packet. Call_Data_t.start_time is the first transmission's start
    // instead (call_concluder.cc:1180-1183), so it's used only for a call the
    // plugin never saw start.
    int64_t start_time = info.start_time;
    bool known = false;
    {
      std::lock_guard<std::mutex> lock(mu_);
      auto it = calls_.find({info.sys_num, info.call_num});
      if (it != calls_.end()) {
        start_time = it->second.start_time;
        known = true;
        calls_.erase(it);
      }
    }
    if (!known) {
      BOOST_LOG_TRIVIAL(warning) << kLog << "[" << sys->short_name << "] call_end for call " << info.call_num
                                 << " the plugin never saw start; using its first transmission's start time " << start_time;
    }

    json transmissions = json::array();
    for (const Transmission &t : info.transmission_list) {
      transmissions.push_back({{"unit", t.source > 0 ? uint32_t(t.source) : 0u},
                               {"start_time_ms", int64_t(t.start_time_ms)},
                               {"stop_time_ms", int64_t(t.stop_time_ms)}});
    }
    json j = {
        {"sys_num", info.sys_num},
        {"call_num", int64_t(info.call_num)},
        {"start_time", start_time},
        {"short_name", sys->short_name},
        {"talkgroup", int64_t(info.talkgroup)},
        {"start_time_ms", int64_t(info.start_time_ms)},
        {"stop_time_ms", int64_t(info.stop_time_ms)},
        {"freq", hz(info.freq)},
        {"tdma_slot", slot_of(info.phase2_tdma, info.tdma_slot)},
        {"patched_talkgroups", others(info.patched_talkgroups, info.talkgroup)},
        {"filename", handoff(info.filename, *sys)},
        {"transmissions", transmissions},
    };
    std::string payload = j.dump();
    // A call with a great many transmissions could pass the datagram limit.
    // Drop transmissions from the end and say so, rather than lose the call.
    if (payload.size() > wire::kMaxPayload) {
      j["transmissions_truncated"] = true;
      while (payload.size() > wire::kMaxPayload && !j["transmissions"].empty()) {
        j["transmissions"].erase(j["transmissions"].size() - 1);
        payload = j.dump();
      }
      BOOST_LOG_TRIVIAL(warning) << kLog << "[" << sys->short_name << "] call " << info.call_num << " has "
                                 << info.transmission_list.size() << " transmissions; sent the first " << j["transmissions"].size();
    }
    send(wire::kTypeCallEnd, payload);
    return 0;
  }

  // About every 3 s from the main loop, before trunk-recorder resets the
  // systems' message counts (monitor_systems.cc:770-777, 949-952).
  int system_rates(std::vector<System *> systems, float timeDiff) override {
    json list = json::array();
    for (System *s : systems) {
      const SystemInfo *sys = system_of(s->get_sys_num());
      if (!sys || !sys->supported) continue;
      const double rate = timeDiff > 0 ? double(s->get_message_count()) / double(timeDiff) : 0.0;
      list.push_back({{"sys_num", s->get_sys_num()},
                      {"short_name", sys->short_name},
                      {"system_type", sys->type},
                      {"decode_rate", std::round(rate * 10.0) / 10.0}});
    }
    json j = {{"time_ms", now_ms()}, {"plugin_version", RADIOARCHIVE_PLUGIN_VERSION}, {"systems", list}};
    send(wire::kTypeHeartbeat, j.dump());
    return 0;
  }

  static boost::shared_ptr<RadioArchivePlugin> create() {
    return boost::shared_ptr<RadioArchivePlugin>(new RadioArchivePlugin());
  }

 private:
  // Called before start(), from trunk-recorder's main thread. Afterwards the
  // map is only read.
  void learn_systems(const std::vector<System *> &systems) {
    for (System *s : systems) {
      const int n = s->get_sys_num();
      if (systems_.count(n)) continue;
      SystemInfo info;
      info.short_name = s->get_short_name();
      info.type = s->get_system_type();
      if (info.type != "p25") {
        BOOST_LOG_TRIVIAL(warning) << kLog << "[" << info.short_name << "] system type " << info.type
                                   << " isn't supported (v1 is P25 trunked systems only); its calls aren't sent";
      } else if (info.short_name.empty() || info.short_name.size() > wire::kMaxShortName) {
        BOOST_LOG_TRIVIAL(error) << kLog << "[" << info.short_name << "] shortName is " << info.short_name.size()
                                 << " bytes, over " << wire::kMaxShortName << "; its calls aren't sent";
      } else {
        info.supported = true;
        BOOST_LOG_TRIVIAL(info) << kLog << "[" << info.short_name << "] sys_num " << n << " (p25): sending its calls";
        if (!s->get_audio_archive() && handoff_dir_.empty()) {
          BOOST_LOG_TRIVIAL(warning) << kLog << "[" << info.short_name
                                     << "] audioArchive is off, so trunk-recorder deletes each recording as soon as call_end returns, before the recorder client can read it";
        }
      }
      systems_[n] = info;
    }
  }

  // At start: say whether handoffDir looks usable. Each call_end tries the
  // link anyway, so a directory created later still works.
  void check_handoff_dir() {
    if (handoff_dir_.empty()) return;
    struct stat st {};
    if (stat(handoff_dir_.c_str(), &st) != 0 || !S_ISDIR(st.st_mode)) {
      BOOST_LOG_TRIVIAL(error) << kLog << "handoffDir " << handoff_dir_ << " isn't a directory ("
                               << std::strerror(errno) << "); until it is, call_end names trunk-recorder's own files";
      return;
    }
    if (!capture_dir_.empty() && different_mounts(handoff_dir_, capture_dir_)) {
      BOOST_LOG_TRIVIAL(error) << kLog << "handoffDir " << handoff_dir_ << " is on a different mount from captureDir " << capture_dir_
                               << ", so recordings can't be hard-linked into it; call_end names trunk-recorder's own files";
      return;
    }
    BOOST_LOG_TRIVIAL(info) << kLog << "hard-linking each recording into " << handoff_dir_ << " for the recorder client";
  }

  // In call_end, before it returns and trunk-recorder may delete the file:
  // hard-links the recording into handoffDir under a name of its own and
  // returns the link's path, or the original path when handoffDir isn't set
  // or the link fails. Never copies and never waits.
  std::string handoff(const std::string &file, const SystemInfo &sys) {
    if (handoff_dir_.empty() || file.empty()) return file;
    int err = 0;
    for (int attempt = 0; attempt < 3; attempt++) {
      char prefix[64];
      std::snprintf(prefix, sizeof(prefix), "_%08x_%llu_", boot_id_,
                    (unsigned long long)handoff_seq_.fetch_add(1, std::memory_order_relaxed));
      const std::string link_path = handoff_dir_ + "/" + sys.short_name + prefix + base_name(file);
      if (link(file.c_str(), link_path.c_str()) == 0) return link_path;
      err = errno;
      if (err != EEXIST) break;
    }
    const uint64_t failed = ++handoff_failures_;
    const int64_t now = now_ms();
    int64_t last = last_handoff_log_ms_.load();
    if (now - last >= 60000 && last_handoff_log_ms_.compare_exchange_strong(last, now)) {
      BOOST_LOG_TRIVIAL(warning) << kLog << "[" << sys.short_name << "] can't hard-link " << file << " into " << handoff_dir_ << " ("
                                 << std::strerror(err) << "); sent trunk-recorder's own filename. " << failed << " failed links so far";
    }
    return file;
  }

  const SystemInfo *system_of(int sys_num) const {
    auto it = systems_.find(sys_num);
    return it == systems_.end() ? nullptr : &it->second;
  }

  // mu_ held.
  CallState &state_for(Call *call) {
    const int64_t now = now_ms();
    auto key = std::make_pair(call->get_sys_num(), call->get_call_num());
    auto it = calls_.find(key);
    if (it == calls_.end()) {
      forget_old(now);
      CallState st;
      st.start_time = int64_t(call->get_start_time());
      it = calls_.emplace(key, st).first;
    }
    it->second.touched_ms = now;
    return it->second;
  }

  // mu_ held.
  void forget_old(int64_t now) {
    for (auto it = calls_.begin(); it != calls_.end();) {
      if (now - it->second.touched_ms > kForgetCallMs) {
        it = calls_.erase(it);
      } else {
        ++it;
      }
    }
  }

  // mu_ held.
  std::string transmission_json(Call *call, const CallState &st, const SystemInfo &sys) {
    json j = {
        {"sys_num", call->get_sys_num()},
        {"call_num", int64_t(call->get_call_num())},
        {"start_time", st.start_time},
        {"short_name", sys.short_name},
        {"talkgroup", int64_t(call->get_talkgroup())},
        {"unit", st.last_unit},
        {"time_ms", now_ms()},
        {"freq", hz(call->get_freq())},
        {"tdma_slot", slot_of(call->get_phase2_tdma(), call->get_tdma_slot())},
        {"patched_talkgroups", st.patched},
    };
    return j.dump();
  }

  uint32_t next_seq() { return seq_.fetch_add(1, std::memory_order_relaxed); }

  void send(uint8_t type, const std::string &payload) {
    std::vector<uint8_t> d = wire::control(type, boot_id_, next_seq(), payload);
    if (d.empty()) {
      BOOST_LOG_TRIVIAL(error) << kLog << "a type " << int(type) << " message of " << payload.size() << " bytes doesn't fit a datagram; dropped";
      return;
    }
    send_datagram(d);
  }

  void send_datagram(const std::vector<uint8_t> &d) {
    if (fd_ < 0) return;
    ssize_t n = sendto(fd_, d.data(), d.size(), MSG_DONTWAIT, reinterpret_cast<const sockaddr *>(&dest_), sizeof(dest_));
    if (n == ssize_t(d.size())) return;
    // ECONNREFUSED (no client listening) and EAGAIN (buffer full) are normal.
    const uint64_t failed = ++send_failures_;
    const int64_t now = now_ms();
    int64_t last = last_failure_log_ms_.load();
    if (now - last >= 60000 && last_failure_log_ms_.compare_exchange_strong(last, now)) {
      BOOST_LOG_TRIVIAL(warning) << kLog << "sendto 127.0.0.1:" << port_ << " failed (" << std::strerror(errno) << "); " << failed << " failed sends so far";
    }
  }

  uint16_t port_ = kDefaultPort;
  int fd_ = -1;
  sockaddr_in dest_{};
  uint32_t boot_id_ = 0;
  std::atomic<uint32_t> seq_{0};
  std::atomic<uint64_t> send_failures_{0};
  std::atomic<int64_t> last_failure_log_ms_{0};

  std::string handoff_dir_;  // "" when off; set before start(), then only read
  std::string capture_dir_;
  std::atomic<uint64_t> handoff_seq_{0};
  std::atomic<uint64_t> handoff_failures_{0};
  std::atomic<int64_t> last_handoff_log_ms_{0};

  std::map<int, SystemInfo> systems_;

  std::mutex mu_;
  std::map<std::pair<int, long>, CallState> calls_;
  std::set<int> wrong_rate_logged_;  // systems already logged
};

}  // namespace

BOOST_DLL_ALIAS(RadioArchivePlugin::create, create_plugin)
