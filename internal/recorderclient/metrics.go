package recorderclient

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Metric names (SPEC.md R90; the UDP ones are ADR 0002's).
const (
	MetricMalformed   = "radioarchive_client_udp_malformed_total"    // {reason}
	MetricUnknownType = "radioarchive_client_udp_unknown_type_total" //
	MetricLost        = "radioarchive_client_udp_lost_total"         //
	MetricLate        = "radioarchive_client_udp_late_audio_total"   //
	MetricPatchCopies = "radioarchive_client_patch_copies_total"     //
	MetricLiveDropped = "radioarchive_client_live_dropped_total"     // {reason}
	MetricUploads     = "radioarchive_client_uploads_total"          // {result}

	// R5's queue. The two gauges are the heartbeat's queue count and age.
	MetricQueueEntries   = "radioarchive_client_queue_entries"            // gauge
	MetricQueueOldestAge = "radioarchive_client_queue_oldest_age_seconds" // gauge
	MetricQueueExpired   = "radioarchive_client_queue_expired_total"      // dropped, older than QUEUE_MAX_AGE
	MetricQueueErrors    = "radioarchive_client_queue_errors_total"       // {op}: read, write, remove
)

// Metrics are the client's counters and gauges, served in Prometheus text
// format.
type Metrics struct {
	mu sync.Mutex
	c  map[string]uint64         // name or name{k="v"}
	g  map[string]func() float64 // gauges, by name
}

// NewMetrics returns empty counters.
func NewMetrics() *Metrics {
	return &Metrics{c: map[string]uint64{}, g: map[string]func() float64{}}
}

func key(name string, labels []string) string {
	if len(labels) == 0 {
		return name
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i := 0; i+1 < len(labels); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", labels[i], labels[i+1])
	}
	b.WriteByte('}')
	return b.String()
}

// Add adds n to a counter. labels are name, value pairs.
func (m *Metrics) Add(n uint64, name string, labels ...string) {
	m.mu.Lock()
	m.c[key(name, labels)] += n
	m.mu.Unlock()
}

// Inc adds 1.
func (m *Metrics) Inc(name string, labels ...string) { m.Add(1, name, labels...) }

// Get returns one counter.
func (m *Metrics) Get(name string, labels ...string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.c[key(name, labels)]
}

// Sum returns a counter's total over all its labels.
func (m *Metrics) Sum(name string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n uint64
	for k, v := range m.c {
		if k == name || strings.HasPrefix(k, name+"{") {
			n += v
		}
	}
	return n
}

// WriteText writes the counters in Prometheus text format.
func (m *Metrics) WriteText(w io.Writer) {
	m.mu.Lock()
	keys := make([]string, 0, len(m.c))
	for k := range m.c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]uint64, len(keys))
	for i, k := range keys {
		vals[i] = m.c[k]
	}
	gnames := make([]string, 0, len(m.g))
	for k := range m.g {
		gnames = append(gnames, k)
	}
	sort.Strings(gnames)
	gfuncs := make([]func() float64, len(gnames))
	for i, k := range gnames {
		gfuncs[i] = m.g[k]
	}
	m.mu.Unlock()
	typed := map[string]bool{}
	for i, k := range keys {
		name, _, _ := strings.Cut(k, "{")
		if !typed[name] {
			fmt.Fprintf(w, "# TYPE %s counter\n", name)
			typed[name] = true
		}
		fmt.Fprintf(w, "%s %d\n", k, vals[i])
	}
	for i, name := range gnames {
		fmt.Fprintf(w, "# TYPE %s gauge\n%s %g\n", name, name, gfuncs[i]())
	}
}

// SetGauge registers a gauge, read when the metrics are served.
func (m *Metrics) SetGauge(name string, f func() float64) {
	m.mu.Lock()
	m.g[name] = f
	m.mu.Unlock()
}

// Gauge reads a gauge (0 if it isn't registered).
func (m *Metrics) Gauge(name string) float64 {
	m.mu.Lock()
	f := m.g[name]
	m.mu.Unlock()
	if f == nil {
		return 0
	}
	return f()
}

// ServeHTTP serves /metrics.
func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	m.WriteText(w)
}
