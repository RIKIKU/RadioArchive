package recorderclient

import "math"

// LiveLoudness is the integrated loudness, in LUFS, the client levels each
// call's live audio to, so that it plays as loud as trunk-recorder v5's
// recordings (ADR 0047). Their loudnorm aims at -16 LUFS but also holds the
// true peak under -0.1 dBTP, and P25 voice peaks about 20 dB over its
// loudness, so they land lower: 47 calls from a real system measured -17.1
// to -22.5 LUFS, -19.5 on average. The leveller's gain follows the call as
// it goes, which raises its loudness about a LU over this target, to match.
const LiveLoudness = -20.0

// Loudness is pcm's integrated loudness in LUFS, as ITU-R BS.1770-4 (and
// ffmpeg's loudnorm, which levels trunk-recorder's recordings) measures it:
// K-weighted, in 400 ms blocks overlapping by 75%, gated at -70 LUFS and
// then at 10 LU under the mean of the blocks that passed. It's -Inf for
// silence.
func Loudness(pcm []int16, rate int) float64 {
	m := newMeter(rate)
	for _, v := range pcm {
		m.add(float64(v) / 32768)
	}
	m.flush()
	return m.integrated()
}

// LevelLive levels a call's live PCM as the engine does, 20 ms at a time.
func LevelLive(pcm []int16, rate int) []int16 {
	out := append([]int16(nil), pcm...)
	l := newLeveler(rate)
	n := rate / 50
	for off := 0; off < len(out); off += n {
		l.level(out[off:min(off+n, len(out))])
	}
	return out
}

// The leveller's limits.
const (
	maxGainDB = 30.0  // a quiet call's most: more is a noise floor, not speech
	minGainDB = -20.0 // a loud call's least
	riseDB    = 1.0   // the gain rises at most this much a frame (50 dB/s); it falls at once
)

// ceiling is the peak a levelled sample may reach: -1 dBFS, below
// loudnorm's -0.1 dBTP because the Opus encoder can overshoot a little.
var ceiling = math.Pow(10, -1.0/20) * 32767

// leveler brings one call's live audio to LiveLoudness as it arrives, so
// that it plays as loud as the call's recording (ADR 0047). loudnorm
// measures the whole call before it renders it; live audio can't wait for
// the end, so the gain follows the call's loudness so far, measured the same
// way. It rises smoothly, falls at once, and is held down for any frame that
// would otherwise peak over the ceiling.
type leveler struct {
	m       *meter
	gainDB  float64 // the gain the call's loudness so far asks for, smoothed
	started bool    // any audio over the gate yet
	applied float64 // the linear gain the last frame ended at
}

func newLeveler(rate int) *leveler { return &leveler{m: newMeter(rate), applied: 1} }

// level levels one frame of PCM in place.
func (l *leveler) level(frame []int16) {
	peak := 0.0
	for _, v := range frame {
		l.m.add(float64(v) / 32768)
		peak = max(peak, math.Abs(float64(v)))
	}
	if est := l.m.estimate(); !math.IsInf(est, -1) {
		want := min(maxGainDB, max(minGainDB, LiveLoudness-est))
		if !l.started {
			l.gainDB, l.started = want, true
		} else {
			l.gainDB = min(want, l.gainDB+riseDB)
		}
	}
	end := math.Pow(10, l.gainDB/20)
	start := l.applied
	if peak > 0 {
		end = min(end, ceiling/peak)
		start = min(start, ceiling/peak)
	}
	// A ramp across the frame, so the gain never steps.
	n := float64(len(frame))
	for i, v := range frame {
		g := start + (end-start)*float64(i+1)/n
		frame[i] = int16(max(-32768, min(32767, math.Round(float64(v)*g))))
	}
	l.applied = end
}

// kWeight is BS.1770's K-weighting: a high shelf then a high pass, two
// biquads designed for any sample rate as libebur128 does.
type kWeight struct {
	b1, a1, b2, a2 [3]float64
	x1, y1, x2, y2 [2]float64
}

func newKWeight(rate int) *kWeight {
	fs := float64(rate)
	k := &kWeight{}
	{
		f0, g, q := 1681.974450955533, 3.999843853973347, 0.7071752369554196
		K := math.Tan(math.Pi * f0 / fs)
		vh := math.Pow(10, g/20)
		vb := math.Pow(vh, 0.4996667741545416)
		a0 := 1 + K/q + K*K
		k.b1 = [3]float64{(vh + vb*K/q + K*K) / a0, 2 * (K*K - vh) / a0, (vh - vb*K/q + K*K) / a0}
		k.a1 = [3]float64{1, 2 * (K*K - 1) / a0, (1 - K/q + K*K) / a0}
	}
	{
		f0, q := 38.13547087602444, 0.5003270373238773
		K := math.Tan(math.Pi * f0 / fs)
		a0 := 1 + K/q + K*K
		k.b2 = [3]float64{1, -2, 1}
		k.a2 = [3]float64{1, 2 * (K*K - 1) / a0, (1 - K/q + K*K) / a0}
	}
	return k
}

func biquad(b, a [3]float64, x, y *[2]float64, in float64) float64 {
	out := b[0]*in + b[1]*x[0] + b[2]*x[1] - a[1]*y[0] - a[2]*y[1]
	x[1], x[0] = x[0], in
	y[1], y[0] = y[0], out
	return out
}

func (k *kWeight) filter(v float64) float64 {
	return biquad(k.b2, k.a2, &k.x2, &k.y2, biquad(k.b1, k.a1, &k.x1, &k.y1, v))
}

// meter measures BS.1770 loudness as samples arrive: K-weighted energy in
// 100 ms steps, each 400 ms block the mean of the last four steps.
type meter struct {
	k      *kWeight
	step   int       // samples in 100 ms
	sum    float64   // K-weighted energy of the step so far
	n      int       // samples in it
	steps  []float64 // the last (up to) four steps' mean squares
	blocks []float64 // every block's mean square that passed the -70 LUFS gate

	cached  float64 // integrated() of the first cachedN blocks
	cachedN int
}

func newMeter(rate int) *meter {
	return &meter{k: newKWeight(rate), step: rate / 10}
}

const absoluteGate = -70.0

func lufs(ms float64) float64 { return -0.691 + 10*math.Log10(ms) }

func (m *meter) add(v float64) {
	f := m.k.filter(v)
	m.sum += f * f
	m.n++
	if m.n == m.step {
		m.endStep()
	}
}

func (m *meter) endStep() {
	m.steps = append(m.steps, m.sum/float64(m.n))
	if len(m.steps) > 4 {
		m.steps = m.steps[1:]
	}
	m.sum, m.n = 0, 0
	if len(m.steps) == 4 {
		m.block(m.steps)
	}
}

func (m *meter) block(steps []float64) {
	var ms float64
	for _, s := range steps {
		ms += s
	}
	ms /= float64(len(steps))
	if lufs(ms) > absoluteGate {
		m.blocks = append(m.blocks, ms)
	}
}

// flush ends the measurement: audio shorter than one block is measured as
// one block of what there is.
func (m *meter) flush() {
	if len(m.blocks) == 0 && len(m.steps) < 4 {
		steps := m.steps
		if m.n > 0 {
			steps = append(steps, m.sum/float64(m.n))
		}
		if len(steps) > 0 {
			m.block(steps)
		}
	}
}

// estimate is the loudness so far: integrated, or before the first whole
// block, that of the audio there is.
func (m *meter) estimate() float64 {
	if len(m.blocks) > 0 {
		return m.integrated()
	}
	var sum float64
	var n int
	for _, s := range m.steps {
		sum += s * float64(m.step)
		n += m.step
	}
	sum += m.sum
	n += m.n
	if n == 0 || sum == 0 || lufs(sum/float64(n)) <= absoluteGate {
		return math.Inf(-1)
	}
	return lufs(sum / float64(n))
}

// integrated is the loudness of the blocks so far, gated.
func (m *meter) integrated() float64 {
	if len(m.blocks) == 0 {
		return math.Inf(-1)
	}
	if m.cachedN == len(m.blocks) {
		return m.cached
	}
	var sum float64
	for _, b := range m.blocks {
		sum += b
	}
	rel := lufs(sum/float64(len(m.blocks))) - 10
	var gated float64
	var n int
	for _, b := range m.blocks {
		if lufs(b) > rel {
			gated += b
			n++
		}
	}
	m.cached, m.cachedN = lufs(gated/float64(n)), len(m.blocks)
	return m.cached
}
