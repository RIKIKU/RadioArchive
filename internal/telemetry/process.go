package telemetry

import (
	"context"
	"runtime"
	"time"
)

// Process writes the build's version and the Go runtime's memory and
// goroutines, for the resource budget (section 7, A37) on a dashboard.
func Process(version string, started time.Time) Collector {
	return CollectorFunc(func(_ context.Context, w *Writer) {
		w.Set(BuildInfo, 1, version)
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		w.Set(MemoryBytes, float64(m.HeapInuse), "heap")
		w.Set(MemoryBytes, float64(m.Sys), "sys")
		w.Set(Goroutines, float64(runtime.NumGoroutine()))
		w.Time(StartTime, started)
	})
}
