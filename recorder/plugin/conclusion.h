// What trunk-recorder v5.2.1 does with a recorded call when it concludes it,
// as far as the plugin is concerned: hand it to call_end, or drop it without
// a recording and without calling any plugin (ADR 0053). It mirrors
// Call_Concluder::conclude_call (call_concluder.cc:1199-1239) and the
// transmission filter in create_call_data (call_concluder.cc:1109-1124),
// with no trunk-recorder types, so recorder/plugin/conclusion_test.cc can
// test it with nothing but a compiler.
#ifndef RADIOARCHIVE_CONCLUSION_H
#define RADIOARCHIVE_CONCLUSION_H

#include <algorithm>
#include <cstdint>
#include <vector>

namespace radioarchive {

enum class Conclusion {
  kCallEnd,          // a worker renders the recording, then call_end
  kSuperseded,       // "Call has been superseded. Removing files."
  kEncrypted,        // files removed
  kNoTransmissions,  // "No Transmissions were recorded!"
  kTooShort,         // "is less than min duration"
};

// The name a withdrawal gives as its reason.
inline const char *reason(Conclusion c) {
  switch (c) {
    case Conclusion::kSuperseded: return "superseded";
    case Conclusion::kEncrypted: return "encrypted";
    case Conclusion::kNoTransmissions: return "no_transmissions";
    case Conclusion::kTooShort: return "too_short";
    default: return "";
  }
}

// One transmission's span, from Transmission::start_time_ms and stop_time_ms.
struct Span {
  int64_t start_ms;
  int64_t stop_ms;
};

// How trunk-recorder concludes a call, from what it reads there: whether the
// call was superseded or is encrypted, its recorder's transmission list, and
// the system's minTransmissionDuration and minDuration (seconds).
inline Conclusion conclude(bool superseded, bool encrypted, const std::vector<Span> &transmissions,
                           double min_tx_s, double min_duration_s) {
  if (superseded) return Conclusion::kSuperseded;
  if (encrypted) return Conclusion::kEncrypted;
  int64_t audio_ms = 0;
  bool any = false;
  for (const Span &t : transmissions) {
    const int64_t ms = std::max<int64_t>(0, t.stop_ms - t.start_ms);
    if (ms / 1000.0 < min_tx_s) continue;  // removed as shorter than minTransmissionDuration
    any = true;
    audio_ms += ms;
  }
  if (!any) return Conclusion::kNoTransmissions;
  if (audio_ms / 1000.0 <= min_duration_s) return Conclusion::kTooShort;
  return Conclusion::kCallEnd;
}

}  // namespace radioarchive

#endif
