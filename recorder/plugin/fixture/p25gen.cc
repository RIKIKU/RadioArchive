// p25gen writes a synthetic P25 Phase 1 trunked system as a SigMF recording
// for trunk-recorder's "sigmf" source, so CI can run the real trunk-recorder
// and the recorder plugin without a radio. Nothing in it is real radio
// traffic: the system, talkgroups and units are the spec's made-up test
// fixture (SPEC.md section 8), and the voice is IMBE-encoded tones.
//
//   p25gen <dir>
//
// writes <dir>/fixture.sigmf-data and <dir>/fixture.sigmf-meta. (The
// "iqfile" source would be simpler, but in v5.2.1 config.cc passes its
// center, rate and repeat to the Source constructor in the wrong order, so
// it runs at a sample rate of 0.)
//
// The data is complex float32 (cf32_le, GNU Radio's gr_complex) at 240 kHz,
// centred on 851.000 MHz, holding two C4FM channels:
//
//   control channel 850.950 MHz  TSDUs back to back: IDEN_UP (band plan),
//                                RFSS and network status, the grants below,
//                                and a Motorola patch of 1002 with 1001
//   voice channel   851.050 MHz  (channel 1-8 in that band plan) the calls
//
// The scenario (seconds from the start of the file):
//
//   3.0   call 1, talkgroup 1001: unit 5001 talks 3.3-6.2, then unit 5123
//         talks 6.9-9.1 (two transmissions, one call)
//   ~13   trunk-recorder concludes call 1 (3 s callTimeout after the last
//         grant update)
//   15.0  patch: supergroup 1002 with 1001, refreshed to the end. It starts
//         after call 1 is concluded because trunk-recorder looks patches
//         up when it concludes a call, not when it was made
//         (call_concluder.cc:1079)
//   16.0  call 2, talkgroup 1002 (patched with 1001): unit 5101, 16.2-18.4
//   22.0  call 3, talkgroup 1003: granted to unit 5201 until 23.5, but the
//         voice channel stays silent, so trunk-recorder records no
//         transmissions and drops the call when it concludes it (~26.5)
//         without a call_end: the plugin withdraws it (ADR 0053)
//   28.0  end; trunk-recorder exits about 10 s later, when it notices the
//         source has stopped
//
// recorder/plugin/plugintest checks what the plugin sends for it.
//
// It's built from trunk-recorder's own P25 library (the encoders and bit
// tables in lib/op25_repeater) against the pinned release, so the frames are
// laid out exactly as trunk-recorder's decoder reads them.

#include <cmath>
#include <complex>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <random>
#include <string>
#include <vector>

#include "ezpwd/rs"
#include "imbe_vocoder/imbe_vocoder.h"
#include "op25_imbe_frame.h"
#include "op25_p25_frame.h"

namespace {

// ---- The made-up system ----------------------------------------------------

constexpr uint32_t kNAC = 0x293;
constexpr double kSampleRate = 240000.0;
constexpr double kCenter = 851000000.0;
constexpr double kControlFreq = 850950000.0;
constexpr double kBandBase = 850950000.0;  // IDEN_UP base: channel 0
constexpr double kBandStep = 12500.0;
constexpr int kIden = 1;
constexpr int kVoiceChannel = 8;  // 850.950 + 8 x 12.5 kHz = 851.050 MHz
constexpr double kVoiceFreq = kBandBase + kVoiceChannel * kBandStep;
constexpr uint32_t kSysID = 0x1A1;
constexpr uint32_t kWACN = 0xBEE01;
constexpr double kDuration = 28.0;

constexpr double kSymbolRate = 4800.0;
constexpr int kSps = int(kSampleRate / kSymbolRate);  // 50 samples per symbol

struct Tx {
  uint32_t unit;
  double start, end;  // seconds; voice frames are sent from start until end
  bool voice = true;  // false: granted, but nothing on the voice channel
};

struct CallPlan {
  uint16_t talkgroup;
  double grant;  // first grant on the control channel
  std::vector<Tx> txs;
};

const std::vector<CallPlan> kCalls = {
    {1001, 3.0, {{5001, 3.3, 6.2}, {5123, 6.9, 9.1}}},
    {1002, 16.0, {{5101, 16.2, 18.4}}},
    {1003, 22.0, {{5201, 22.3, 23.5, false}}},
};
constexpr double kPatchFrom = 15.0;
constexpr uint16_t kPatchSG = 1002, kPatchMember = 1001;

// ---- Bits --------------------------------------------------------------

typedef std::vector<bool> bits;

void put(bits &b, uint64_t v, int n) {
  for (int i = n - 1; i >= 0; i--) b.push_back((v >> i) & 1);
}

// NID: NAC and DUID, BCH(63,16,23) with generator 6331 1413 6723 5453
// (octal), then a parity bit that is 1 for LDU1 and LDU2 and 0 otherwise
// (TIA-102.BAAC; p25_framer.cc checks it).
uint64_t nid(uint32_t nac, uint32_t duid) {
  const uint64_t g = 06331141367235453ULL;
  const uint64_t info = (uint64_t(nac) << 4) | duid;
  uint64_t r = info << 47;
  for (int i = 62; i >= 47; i--) {
    if (r & (1ULL << i)) r ^= g << (i - 47);
  }
  const uint64_t cw = (info << 47) | r;
  return (cw << 1) | ((duid == 5 || duid == 10) ? 1 : 0);
}

// Inserts a status dibit (1,0) after every 35 dibits, counted from the start
// of the frame sync, as process_blocks() and the LDU tables expect.
bits with_status(const bits &data) {
  bits out;
  size_t d = 0;
  for (size_t i = 0; i + 1 < data.size(); i += 2) {
    if ((d + 1) % 36 == 0) {
      out.push_back(1);
      out.push_back(0);
      d++;
    }
    out.push_back(data[i]);
    out.push_back(data[i + 1]);
    d++;
  }
  return out;
}

bits header(uint32_t duid) {
  bits b;
  put(b, P25_FRAME_SYNC_MAGIC, 48);
  put(b, nid(kNAC, duid), 64);
  return b;
}

// CRC-CCITT as p25p1_fdma.cc checks it: the 12 bytes must give 0. The two
// CRC bytes are found by search, so this can't disagree with the decoder.
uint16_t tsbk_crc(const uint8_t *buf) {
  uint32_t poly = (1 << 12) + (1 << 5) + 1, crc = 0;
  for (int i = 0; i < 12; i++) {
    for (int j = 0; j < 8; j++) {
      crc = ((crc << 1) | ((buf[i] >> (7 - j)) & 1)) & 0x1ffff;
      if (crc & 0x10000) crc = (crc & 0xffff) ^ poly;
    }
  }
  return (crc ^ 0xffff) & 0xffff;
}

struct TSBK {
  uint8_t b[12] = {0};
};

// A TSBK from a 96-bit value laid out as p25_parser.cc reads it: byte k is
// bits 88-8k .. 95-8k, so the opcode is the top byte.
struct Fields {
  unsigned __int128 v = 0;
  void set(int shift, uint64_t value, int width) {
    unsigned __int128 mask = ((unsigned __int128)1 << width) - 1;
    v &= ~(mask << shift);
    v |= ((unsigned __int128)value & mask) << shift;
  }
};

// Fills in bytes 10-11 so the block's CRC checks.
void seal(TSBK &t) {
  for (uint32_t c = 0; c < 65536; c++) {
    t.b[10] = uint8_t(c >> 8);
    t.b[11] = uint8_t(c);
    if (tsbk_crc(t.b) == 0) return;
  }
  std::fprintf(stderr, "p25gen: no CRC for TSBK opcode %02x\n", t.b[0] & 0x3f);
  std::exit(1);
}

TSBK make_tsbk(uint8_t opcode, uint8_t mfid, const Fields &f) {
  TSBK t;
  Fields all = f;
  all.set(88, opcode & 0x3f, 6);
  all.set(80, mfid, 8);
  for (int k = 0; k < 10; k++) t.b[k] = uint8_t(all.v >> (88 - 8 * k));
  seal(t);
  return t;
}

uint16_t channel_id(int ch) { return uint16_t((kIden << 12) | ch); }

TSBK iden_up() {
  Fields f;
  f.set(76, kIden, 4);
  f.set(67, uint64_t(12500 / 125), 9);  // bandwidth, 0.125 kHz units
  f.set(58, 0, 9);  // transmit offset 0
  f.set(48, uint64_t(kBandStep / 125), 10);  // spacing, 0.125 kHz units
  f.set(16, uint64_t(kBandBase / 5), 32);  // base, 5 Hz units
  return make_tsbk(0x3d, 0, f);
}

TSBK rfss_status() {
  Fields f;
  f.set(56, kSysID, 12);
  f.set(48, 1, 8);  // RFSS 1
  f.set(40, 1, 8);  // site 1
  f.set(24, channel_id(0), 16);
  return make_tsbk(0x3a, 0, f);
}

TSBK net_status() {
  Fields f;
  f.set(52, kWACN, 20);
  f.set(40, kSysID, 12);
  f.set(24, channel_id(0), 16);
  return make_tsbk(0x3b, 0, f);
}

TSBK grant(uint16_t tg, uint32_t src) {
  Fields f;
  f.set(72, 0x00, 8);  // service options: clear, priority 0
  f.set(56, channel_id(kVoiceChannel), 16);
  f.set(40, tg, 16);
  f.set(16, src, 24);
  return make_tsbk(0x00, 0, f);
}

TSBK grant_update(uint16_t tg) {
  Fields f;
  f.set(64, channel_id(kVoiceChannel), 16);
  f.set(48, tg, 16);
  f.set(32, channel_id(kVoiceChannel), 16);
  f.set(16, tg, 16);
  return make_tsbk(0x02, 0, f);
}

TSBK patch_add(uint16_t sg, uint16_t ga1) {  // Motorola MOT_GRG_ADD_CMD
  Fields f;
  f.set(64, sg, 16);
  f.set(48, ga1, 16);
  f.set(32, 0, 16);
  f.set(16, 0, 16);
  return make_tsbk(0x00, 0x90, f);
}

// One TSBK: 48 data dibits plus a flush dibit through the rate 1/2 trellis
// (the inverse of block_deinterleave() in p25p1_fdma.cc), then interleaved.
void put_tsbk(bits &frame, const TSBK &t) {
  static const uint8_t next_words[4][4] = {
      {0x2, 0xC, 0x1, 0xF}, {0xE, 0x0, 0xD, 0x3}, {0x9, 0x7, 0xA, 0x4}, {0x5, 0xB, 0x6, 0x8}};
  static const uint16_t tb[] = {
      0,  1,  2,  3,  52, 53, 54, 55, 100, 101, 102, 103, 148, 149, 150, 151, 4,  5,  6,  7,  56, 57, 58, 59, 104,
      105, 106, 107, 152, 153, 154, 155, 8,  9,  10, 11, 60, 61, 62, 63, 108, 109, 110, 111, 156, 157, 158, 159, 12, 13,
      14, 15, 64, 65, 66, 67, 112, 113, 114, 115, 160, 161, 162, 163, 16, 17, 18, 19, 68, 69, 70, 71, 116, 117, 118,
      119, 164, 165, 166, 167, 20, 21, 22, 23, 72, 73, 74, 75, 120, 121, 122, 123, 168, 169, 170, 171, 24, 25, 26, 27,
      76, 77, 78, 79, 124, 125, 126, 127, 172, 173, 174, 175, 28, 29, 30, 31, 80, 81, 82, 83, 128, 129, 130, 131, 176,
      177, 178, 179, 32, 33, 34, 35, 84, 85, 86, 87, 132, 133, 134, 135, 180, 181, 182, 183, 36, 37, 38, 39, 88, 89,
      90, 91, 136, 137, 138, 139, 184, 185, 186, 187, 40, 41, 42, 43, 92, 93, 94, 95, 140, 141, 142, 143, 188, 189, 190,
      191, 44, 45, 46, 47, 96, 97, 98, 99, 144, 145, 146, 147, 192, 193, 194, 195, 48, 49, 50, 51};
  bits coded(196);
  int state = 0;
  for (int d = 0; d < 49; d++) {
    int dibit = d < 48 ? (t.b[d / 4] >> (6 - 2 * (d % 4))) & 3 : 0;
    uint8_t cw = next_words[state][dibit];
    for (int j = 0; j < 4; j++) coded[tb[4 * d + j]] = (cw >> (3 - j)) & 1;
    state = dibit;
  }
  frame.insert(frame.end(), coded.begin(), coded.end());
}

// A TSDU of three TSBKs: 720 bits with status, 75 ms.
bits tsdu(TSBK a, TSBK b, TSBK c) {
  c.b[0] |= 0x80;  // last block, which is under the CRC
  seal(c);
  TSBK blocks[3] = {a, b, c};
  bits frame = header(0x7);
  for (const TSBK &t : blocks) put_tsbk(frame, t);
  return with_status(frame);
}

// TDU: sync, NID and 28 null bits, 144 bits with status.
bits tdu() {
  bits frame = header(0x3);
  for (int i = 0; i < 28; i++) frame.push_back(0);
  return with_status(frame);
}

// 24 hexbits (12 data + 12 RS(24,12,13) parity, or 16 + 8 RS(24,16,9)),
// each with Hamming(10,6,3), into the 240 link control / ESS positions.
void put_hexbits(bits &frame, const std::vector<uint8_t> &data, int nparity) {
  std::vector<uint8_t> hb(39, 0);  // the shortened code's leading zeros
  hb.insert(hb.end(), data.begin(), data.end());
  if (nparity == 12) {
    ezpwd::RS<63, 51> rs;
    rs.encode(hb);
  } else {
    ezpwd::RS<63, 55> rs;
    rs.encode(hb);
  }
  int k = 0;
  for (int i = 39; i < 63; i++) {
    uint32_t cw = (uint32_t(hb[i]) << 4) | hmg1063EncTbl[hb[i]];
    for (int j = 9; j >= 0; j--) frame[imbe_ldu_ls_data_bits[k++]] = (cw >> j) & 1;
  }
}

std::vector<uint8_t> to_hexbits(const uint8_t *bytes, int nbytes) {
  std::vector<uint8_t> out;
  uint32_t acc = 0;
  int n = 0;
  for (int i = 0; i < nbytes; i++) {
    acc = (acc << 8) | bytes[i];
    n += 8;
    while (n >= 6) {
      out.push_back((acc >> (n - 6)) & 63);
      n -= 6;
    }
  }
  return out;
}

// The voice: a tone, IMBE-encoded by trunk-recorder's own vocoder.
struct Voice {
  imbe_vocoder vocoder;
  long sample = 0;
  double hz;
  explicit Voice(double f) : hz(f) {}
  void next(int16_t u[8]) {
    int16_t snd[160];
    for (int i = 0; i < 160; i++, sample++) {
      snd[i] = int16_t(6000.0 * std::sin(2 * M_PI * hz * double(sample) / 8000.0));
    }
    vocoder.imbe_encode(u, snd);
  }
};

// An LDU: nine IMBE codewords, then LDU1's link control (group voice
// channel user: talkgroup and unit) or LDU2's ESS (clear: algorithm 0x80).
bits ldu(bool first, uint16_t tg, uint32_t unit, Voice &voice) {
  bits frame(P25_VOICE_FRAME_SIZE, false);
  for (uint32_t i = 0; i < nof_voice_codewords; i++) {
    int16_t u[8];
    voice.next(u);
    voice_codeword cw(voice_codeword_sz);
    imbe_header_encode(cw, u[0], u[1], u[2], u[3], u[4], u[5], u[6], u[7]);
    imbe_interleave(frame, cw, i);
  }
  if (first) {
    const uint8_t lc[9] = {0x00, 0x00, 0x00, 0x00, uint8_t(tg >> 8), uint8_t(tg),
                           uint8_t(unit >> 16), uint8_t(unit >> 8), uint8_t(unit)};
    put_hexbits(frame, to_hexbits(lc, 9), 12);
  } else {
    uint8_t ess[12] = {0};  // MI 0 (9 bytes), algid 0x80, key 0
    ess[9] = 0x80;
    put_hexbits(frame, to_hexbits(ess, 12), 8);
  }
  p25_setup_frame_header(frame, nid(kNAC, first ? 0x5 : 0xa));
  for (uint16_t pos : imbe_ldu_status_bits) {
    frame[pos] = (pos % 2) == 0;  // status dibit (1,0)
  }
  return frame;
}

// ---- Symbols and modulation --------------------------------------------

// One channel's symbols, 4800 a second: -1 is carrier off.
struct Channel {
  double freq;
  std::vector<int8_t> sym;
  Channel(double f, size_t n) : freq(f), sym(n, -1) {}
  // Writes a frame's dibits at symbol index at; returns the index after it.
  size_t place(size_t at, const bits &frame) {
    for (size_t i = 0; i + 1 < frame.size() && at < sym.size(); i += 2) {
      sym[at++] = int8_t((frame[i] << 1) | frame[i + 1]);
    }
    return at;
  }
};

// C4FM: dibit 01 is +1800 Hz, 00 +600, 10 -600, 11 -1800, through a raised
// cosine (alpha 0.2) Nyquist pulse, FM modulated.
void modulate(const std::vector<Channel> &chans, const char *path) {
  const size_t nsym = chans[0].sym.size();
  const size_t n = nsym * kSps;
  const int span = 8;  // symbols each side
  std::vector<double> rc(2 * span * kSps + 1);
  for (size_t i = 0; i < rc.size(); i++) {
    const double t = (double(i) - span * kSps) / kSps;  // in symbols
    const double a = 0.2;
    double sinc = t == 0 ? 1 : std::sin(M_PI * t) / (M_PI * t);
    double den = 1 - 4 * a * a * t * t;
    rc[i] = std::fabs(den) < 1e-9 ? M_PI / 4 * (std::sin(M_PI / (2 * a)) / (M_PI / (2 * a))) : sinc * std::cos(M_PI * a * t) / den;
  }
  static const double level[4] = {600, 1800, -600, -1800};

  std::vector<std::complex<float>> out(n, 0);
  std::mt19937 rng(25);  // fixed seed: the same file every time
  std::normal_distribution<float> noise(0, 0.005f);
  for (size_t s = 0; s < n; s++) out[s] = {noise(rng), noise(rng)};

  for (const Channel &c : chans) {
    std::vector<double> dev(n, 0), env(n, 0);
    for (size_t k = 0; k < nsym; k++) {
      if (c.sym[k] < 0) continue;
      const double a = level[c.sym[k]];
      const long centre = long(k) * kSps + kSps / 2;
      for (size_t i = 0; i < rc.size(); i++) {
        long s = centre + long(i) - span * kSps;
        if (s >= 0 && size_t(s) < n) dev[s] += a * rc[i];
      }
      for (int i = 0; i < kSps; i++) env[k * kSps + i] = 1;
    }
    // Ramp the carrier on and off over a symbol, not a click.
    std::vector<double> smooth(n, 0);
    double acc = 0;
    for (size_t s = 0; s < n; s++) {
      acc += env[s] - (s >= size_t(kSps) ? env[s - kSps] : 0);
      smooth[s] = acc / kSps;
    }
    const double off = c.freq - kCenter;
    double phase = 0;
    for (size_t s = 0; s < n; s++) {
      phase += 2 * M_PI * (off + dev[s]) / kSampleRate;
      if (phase > M_PI) phase -= 2 * M_PI;
      if (phase < -M_PI) phase += 2 * M_PI;
      out[s] += std::polar(float(0.3 * smooth[s]), float(phase));
    }
  }

  FILE *f = std::fopen(path, "wb");
  if (!f || std::fwrite(out.data(), sizeof(out[0]), n, f) != n || std::fclose(f) != 0) {
    std::perror(path);
    std::exit(1);
  }
}

}  // namespace

int main(int argc, char **argv) {
  if (argc != 2) {
    std::fprintf(stderr, "usage: p25gen <dir>\n");
    return 2;
  }
  // The NID encoder against two NIDs trunk-recorder's library hard-codes
  // for NAC 0x293 (p25p1_voice_encode.cc).
  if (nid(0x293, 0x5) != 0x293555ef2c653437ULL || nid(0x293, 0xa) != 0x293aba93bec26a2bULL) {
    std::fprintf(stderr, "p25gen: NID encoder is wrong\n");
    return 1;
  }

  const size_t nsym = size_t(kDuration * kSymbolRate);
  Channel cc(kControlFreq, nsym), vc(kVoiceFreq, nsym);

  // Voice channel: each transmission is LDU1/LDU2 pairs, then a TDU.
  double tone = 440;
  for (const CallPlan &call : kCalls) {
    for (const Tx &tx : call.txs) {
      if (!tx.voice) continue;
      Voice voice(tone);
      tone += 220;
      size_t at = size_t(tx.start * kSymbolRate);
      bool first = true;
      while (at < size_t(tx.end * kSymbolRate)) {
        at = vc.place(at, ldu(first, call.talkgroup, tx.unit, voice));
        first = !first;
      }
      if (!first) at = vc.place(at, ldu(false, call.talkgroup, tx.unit, voice));  // end on an LDU2
      vc.place(at, tdu());
    }
  }

  // Control channel: one TSDU every 75 ms, carrying what's happening then.
  const TSBK filler[3] = {iden_up(), rfss_status(), net_status()};
  int nf = 0;
  for (size_t at = 0, slot = 0; at < nsym; slot++) {
    const double t = double(at) / kSymbolRate;
    std::vector<TSBK> msgs;
    for (const CallPlan &call : kCalls) {
      const double last = call.txs.back().end;
      if (t < call.grant || t > last) continue;
      const Tx *now = nullptr;
      for (const Tx &tx : call.txs) {
        if (t >= tx.start - 0.3 && t <= tx.end) now = &tx;
      }
      // Grants (with the unit) while someone is talking, updates between.
      msgs.push_back(now ? grant(call.talkgroup, now->unit) : grant_update(call.talkgroup));
    }
    if (t >= kPatchFrom && slot % 4 == 0) msgs.push_back(patch_add(kPatchSG, kPatchMember));
    while (msgs.size() < 3) msgs.push_back(filler[nf++ % 3]);
    at = cc.place(at, tsdu(msgs[0], msgs[1], msgs[2]));
  }

  const std::string dir = argv[1];
  modulate({cc, vc}, (dir + "/fixture.sigmf-data").c_str());
  const std::string meta = dir + "/fixture.sigmf-meta";
  FILE *m = std::fopen(meta.c_str(), "w");
  if (!m) {
    std::perror(meta.c_str());
    return 1;
  }
  std::fprintf(m,
               "{\"global\": {\"core:datatype\": \"cf32_le\", \"core:sample_rate\": %.0f, \"core:version\": \"1.0.0\",\n"
               "  \"core:description\": \"Synthetic P25 Phase 1 test system made by RadioArchive's p25gen. Not real radio traffic.\"},\n"
               " \"captures\": [{\"core:sample_start\": 0, \"core:frequency\": %.0f}],\n"
               " \"annotations\": []}\n",
               kSampleRate, kCenter);
  std::fclose(m);
  std::printf("p25gen: wrote %s/fixture.sigmf-{data,meta}, %.0f s at %.0f Hz\n", dir.c_str(), kDuration, kSampleRate);
  return 0;
}
