// Tests conclusion.h: how the plugin predicts trunk-recorder v5.2.1's
// conclusion of a call (ADR 0053). The plugin CI test covers the case the
// synthetic fixture can make, a call with no transmissions; these cover the
// rest. recorder/plugin/Dockerfile builds and runs it before the plugin, so
// a failure fails the image build:
//
//   g++ -std=c++17 -o conclusion_test conclusion_test.cc && ./conclusion_test
#include "conclusion.h"

#include <cstdio>

using radioarchive::conclude;
using radioarchive::Conclusion;
using radioarchive::Span;

namespace {

int failures = 0;

void expect(const char *name, Conclusion got, Conclusion want) {
  if (got != want) {
    std::printf("FAIL %s: got %d, want %d\n", name, int(got), int(want));
    failures++;
  } else {
    std::printf("ok   %s\n", name);
  }
}

}  // namespace

int main() {
  const std::vector<Span> none;
  const std::vector<Span> two = {{1000, 3900}, {4600, 6800}};  // 2.9 s and 2.2 s
  const std::vector<Span> blip = {{1000, 1200}};               // 0.2 s

  // The production case (issue #185): a recorder started, nothing decoded.
  expect("no transmissions", conclude(false, false, none, 0, 0), Conclusion::kNoTransmissions);
  expect("a normal call", conclude(false, false, two, 0, 0), Conclusion::kCallEnd);

  // minTransmissionDuration removes short transmissions first; a call left
  // with none is "No Transmissions were recorded!" too.
  expect("every transmission under minTransmissionDuration", conclude(false, false, blip, 0.5, 0),
         Conclusion::kNoTransmissions);
  expect("a transmission exactly minTransmissionDuration long is kept", conclude(false, false, blip, 0.2, 0),
         Conclusion::kCallEnd);
  expect("only the short one removed", conclude(false, false, {{1000, 1200}, {2000, 5000}}, 0.5, 0),
         Conclusion::kCallEnd);

  // minDuration compares the kept transmissions' total, and a call that
  // long or shorter is dropped.
  expect("under minDuration", conclude(false, false, two, 0, 6), Conclusion::kTooShort);
  expect("exactly minDuration is dropped", conclude(false, false, two, 0, 5.1), Conclusion::kTooShort);
  expect("over minDuration", conclude(false, false, two, 0, 5), Conclusion::kCallEnd);
  expect("minDuration counts only the kept transmissions", conclude(false, false, {{0, 200}, {1000, 3000}}, 0.5, 2.1),
         Conclusion::kTooShort);
  expect("zero-length transmissions with minDuration 0", conclude(false, false, {{1000, 1000}}, 0, 0),
         Conclusion::kTooShort);
  expect("a transmission that ends before it starts counts as 0 ms", conclude(false, false, {{2000, 1000}}, 0.1, 0),
         Conclusion::kNoTransmissions);

  // Checked before the transmissions, in trunk-recorder's order.
  expect("superseded", conclude(true, false, two, 0, 0), Conclusion::kSuperseded);
  expect("superseded and encrypted", conclude(true, true, none, 0, 0), Conclusion::kSuperseded);
  expect("encrypted", conclude(false, true, two, 0, 0), Conclusion::kEncrypted);

  if (failures) {
    std::printf("%d failed\n", failures);
    return 1;
  }
  std::printf("all passed\n");
  return 0;
}
