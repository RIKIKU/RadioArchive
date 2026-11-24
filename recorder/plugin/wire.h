// The recorder plugin's half of the plugin -> client wire format, ADR 0002
// (docs/adr/0002-recorder-protocol.md, "Plugin -> client"). Pure byte
// layout, with no trunk-recorder types, so it reads straight against the
// ADR's tables. internal/recorderproto is the Go half.
#ifndef RADIOARCHIVE_WIRE_H
#define RADIOARCHIVE_WIRE_H

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace radioarchive {
namespace wire {

constexpr uint8_t kMagic0 = 'R';
constexpr uint8_t kMagic1 = 'A';
constexpr uint8_t kVersion = 1;

constexpr uint8_t kTypeAudio = 0x01;
constexpr uint8_t kTypeTransmission = 0x02;
constexpr uint8_t kTypeCallEnd = 0x03;
constexpr uint8_t kTypeHeartbeat = 0x04;

constexpr size_t kHeaderSize = 16;
constexpr size_t kMaxDatagram = 8192;
constexpr size_t kMaxPayload = kMaxDatagram - kHeaderSize;  // 8,176
constexpr size_t kAudioFixed = 34;
constexpr size_t kMaxShortName = 64;
constexpr size_t kMaxAudioSamples = 4000;  // 8,000 bytes of PCM per datagram

// The fields of one audio datagram except its PCM.
struct AudioHeader {
  int64_t call_num;
  int64_t start_time;  // Unix seconds, Call::get_start_time()
  int32_t talkgroup;
  uint32_t unit;  // 0 when unknown
  uint32_t freq;  // Hz
  uint16_t sys_num;
  uint16_t sample_rate;
  int8_t tdma_slot;  // -1 when not TDMA
  std::string short_name;  // 1-64 bytes
};

inline void put_u16(std::vector<uint8_t> &b, uint16_t v) {
  b.push_back(uint8_t(v));
  b.push_back(uint8_t(v >> 8));
}

inline void put_u32(std::vector<uint8_t> &b, uint32_t v) {
  for (int i = 0; i < 4; i++) b.push_back(uint8_t(v >> (8 * i)));
}

inline void put_u64(std::vector<uint8_t> &b, uint64_t v) {
  for (int i = 0; i < 8; i++) b.push_back(uint8_t(v >> (8 * i)));
}

// Appends the 16-byte envelope.
inline void put_header(std::vector<uint8_t> &b, uint8_t type, uint32_t boot_id, uint32_t seq, size_t length) {
  b.push_back(kMagic0);
  b.push_back(kMagic1);
  b.push_back(kVersion);
  b.push_back(type);
  put_u32(b, boot_id);
  put_u32(b, seq);
  put_u16(b, uint16_t(length));
  put_u16(b, 0);  // reserved
}

// One audio datagram for samples[0, count), count 1-4000. The caller has
// already checked the shortName's length.
inline std::vector<uint8_t> audio(uint32_t boot_id, uint32_t seq, const AudioHeader &h, const int16_t *samples, size_t count) {
  const size_t length = kAudioFixed + h.short_name.size() + 2 * count;
  std::vector<uint8_t> b;
  b.reserve(kHeaderSize + length);
  put_header(b, kTypeAudio, boot_id, seq, length);
  put_u64(b, uint64_t(h.call_num));
  put_u64(b, uint64_t(h.start_time));
  put_u32(b, uint32_t(h.talkgroup));
  put_u32(b, h.unit);
  put_u32(b, h.freq);
  put_u16(b, h.sys_num);
  put_u16(b, h.sample_rate);
  b.push_back(uint8_t(h.tdma_slot));
  b.push_back(uint8_t(h.short_name.size()));
  b.insert(b.end(), h.short_name.begin(), h.short_name.end());
  for (size_t i = 0; i < count; i++) put_u16(b, uint16_t(samples[i]));  // little-endian on any host
  return b;
}

// One control datagram (transmission, call_end or heartbeat) with a JSON
// payload. Empty if the payload is over 8,176 bytes.
inline std::vector<uint8_t> control(uint8_t type, uint32_t boot_id, uint32_t seq, const std::string &json) {
  std::vector<uint8_t> b;
  if (json.size() > kMaxPayload) return b;
  b.reserve(kHeaderSize + json.size());
  put_header(b, type, boot_id, seq, json.size());
  b.insert(b.end(), json.begin(), json.end());
  return b;
}

}  // namespace wire
}  // namespace radioarchive

#endif
