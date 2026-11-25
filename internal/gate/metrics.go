package gate

import (
	"context"

	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// Collect writes the live feed's metrics (R90; ADR 0040). They count; they
// never name a talkgroup or a listener.
func (l *Live) Collect(_ context.Context, w *telemetry.Writer) {
	c := &l.Stat
	w.Set(telemetry.LiveFrames, float64(c.FramesSent.Load()), "sent")
	w.Set(telemetry.LiveFrames, float64(c.FramesDropped.Load()), "dropped")
	for kind, n := range map[string]uint64{
		"slow_closed": c.SlowClosed.Load(), "withdrawn": c.Withdrawn.Load(), "delayed_released": c.Released.Load(),
		"delayed_recovered": c.Recovered.Load(), "check_failed": c.CheckFailed.Load(),
	} {
		w.Set(telemetry.LiveEvents, float64(n), kind)
	}
}
