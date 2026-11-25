package retention

import (
	"context"

	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// Collect writes the recordings volume's and the job's metrics (R88, R90;
// ADR 0040): its size and free space, its state (the 15 % warning, low and
// full), recordings deleted, and the nightly run's last finish.
func (j *Job) Collect(ctx context.Context, w *telemetry.Writer) {
	if d, err := j.Disk(); err == nil {
		w.Set(telemetry.DiskBytes, float64(d.Total))
		w.Set(telemetry.DiskFree, float64(d.Free))
		w.Set(telemetry.DiskMinFree, d.MinFree)
		for _, s := range []DiskState{DiskOK, DiskWarning, DiskLow, DiskFull} {
			w.Bool(telemetry.DiskState, d.State == s, string(s))
		}
	}
	w.Set(telemetry.Retention, float64(j.Stat.Expired.Load()), "expired")
	w.Set(telemetry.Retention, float64(j.Stat.Orphans.Load()), "orphans")
	w.Set(telemetry.Retention, float64(j.Stat.Shortened.Load()), "shortened")
	w.Set(telemetry.VolumeFull, float64(j.Stat.Full.Load()))
	if r, err := j.LastRun(ctx, "nightly"); err == nil && r != nil {
		status := "ok"
		if !r.OK {
			status = "failed"
		}
		w.Time(telemetry.NightlyLast, r.FinishedAt, status)
	}
}
