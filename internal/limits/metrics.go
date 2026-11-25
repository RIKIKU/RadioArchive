package limits

import (
	"context"

	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// Collect writes the limits' metrics (R90, R91; ADR 0040): hits per limit,
// the listener sockets and zips open, and whether the radio is full (R71),
// which the 10-minute alert watches.
func (l *Limits) Collect(_ context.Context, w *telemetry.Writer) {
	for _, k := range []Kind{Requests, Playback, Live, Zip} {
		w.Set(telemetry.LimitHits, float64(l.Hits(k)), k.String())
	}
	open := l.LiveOpen()
	w.Set(telemetry.LiveSockets, float64(open))
	w.Set(telemetry.LiveSocketsLimit, float64(l.cfg.LiveTotal))
	w.Bool(telemetry.LiveFull, open >= l.cfg.LiveTotal)
	w.Set(telemetry.ZipsOpen, float64(l.ZipsOpen()))
}
