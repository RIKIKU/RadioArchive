// Package telemetry is the API's metrics (SPEC.md R90, ADR 0040): Prometheus
// text at /metrics, behind METRICS_TOKEN, for the monitoring stack's Alloy
// to scrape.
//
// Nothing can be exported that isn't on the allowlist. Every metric is a
// Desc in names.go, with its name, type, help and label keys, and every
// label key is one of LabelKeys. A collector writes samples through a
// Writer, which refuses a metric that isn't declared, the wrong number of
// label values, and a value that isn't an ID, a code or a fixed word for
// its key (validValue); a refused sample is dropped and counted in
// radioarchive_telemetry_dropped_total, never written. Talkgroup IDs are
// allowed; talkgroup and unit names never are (P1), and nothing here can
// hold a listener's address, a key or a token: no Desc has a label for
// them, and the values a key takes are checked.
//
// RadioArchive sends no traces and no logs anywhere: its logs are the
// container's, on stderr, and hold no names or addresses either (A32).
package telemetry

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// Type is a metric's Prometheus type.
type Type string

// Metric types.
const (
	Counter   Type = "counter"
	Gauge     Type = "gauge"
	Histogram Type = "histogram"
)

// Desc is one metric on the allowlist.
type Desc struct {
	Name   string
	Type   Type
	Help   string
	Labels []string
	// Buckets are a histogram's upper bounds, in order; +Inf is added.
	Buckets []float64
}

// LabelKeys are every label key a metric may have. A Desc with any other
// is a programmer's mistake, which every test that builds a Registry finds.
var LabelKeys = []string{
	"recorder",       // a recorder's name, as admins gave it (R10); never a talkgroup's or a unit's
	"other_recorder", // the other recorder of an overlapping pair (R13)
	"short_name",     // trunk-recorder's shortName for a system (R12)
	"system",         // a system's ID, a number
	"talkgroup",      // a talkgroup's ID, a number (allowed by P1)
	"reason",         // a fixed word: why a call was rejected, or why a recorder is deaf
	"signal",         // disconnected, deaf or quiet (R14)
	"limit",          // a heavy-use limit's name (R70-R73)
	"status",         // an upload's or a job's outcome, a fixed word
	"result",         // what became of something, a fixed word (sent, dropped)
	"kind",           // a fixed word naming a kind of event
	"state",          // a fixed word naming a state
	"version",        // a version number
}

// Collector writes its samples to w at each scrape.
type Collector interface {
	Collect(ctx context.Context, w *Writer)
}

// CollectorFunc is a function as a Collector.
type CollectorFunc func(ctx context.Context, w *Writer)

// Collect calls f.
func (f CollectorFunc) Collect(ctx context.Context, w *Writer) { f(ctx, w) }

// Registry is the metrics one /metrics serves: every declared metric, and
// the collectors that write them.
type Registry struct {
	descs      []*Desc
	known      map[*Desc]bool
	collectors []Collector
	dropped    atomic.Uint64
}

// NewRegistry returns a registry of descs, the allowlist. A Desc with a
// name that isn't radioarchive_..., a duplicate name, a label key not in
// LabelKeys, or a histogram with no buckets panics.
func NewRegistry(descs []*Desc) *Registry {
	r := &Registry{known: map[*Desc]bool{}}
	names := map[string]bool{}
	for _, d := range append(slices.Clone(descs), droppedDesc) {
		switch {
		case !strings.HasPrefix(d.Name, "radioarchive_") || !validName(d.Name):
			panic(fmt.Sprintf("telemetry: metric name %q isn't radioarchive_ and [a-z0-9_]", d.Name))
		case names[d.Name]:
			panic(fmt.Sprintf("telemetry: metric %q declared twice", d.Name))
		case d.Type != Counter && d.Type != Gauge && d.Type != Histogram:
			panic(fmt.Sprintf("telemetry: metric %q has no type", d.Name))
		case d.Type == Histogram && len(d.Buckets) == 0:
			panic(fmt.Sprintf("telemetry: histogram %q has no buckets", d.Name))
		case d.Help == "":
			panic(fmt.Sprintf("telemetry: metric %q has no help", d.Name))
		}
		for _, k := range d.Labels {
			if !slices.Contains(LabelKeys, k) {
				panic(fmt.Sprintf("telemetry: metric %q has label %q, which isn't on the allowlist", d.Name, k))
			}
		}
		names[d.Name] = true
		r.known[d] = true
		r.descs = append(r.descs, d)
	}
	return r
}

// droppedDesc counts samples the Writer refused.
var droppedDesc = &Desc{Name: "radioarchive_telemetry_dropped_total", Type: Counter,
	Help: "Samples refused by the metrics allowlist: an undeclared metric, the wrong labels, or a value that isn't an ID, code or fixed word."}

// Add adds collectors.
func (r *Registry) Add(cs ...Collector) { r.collectors = append(r.collectors, cs...) }

// Dropped is how many samples the allowlist has refused.
func (r *Registry) Dropped() uint64 { return r.dropped.Load() }

// Writer takes one scrape's samples.
type Writer struct {
	reg     *Registry
	samples map[*Desc][]string
}

// Set writes one sample of a counter or a gauge, its label values in the
// order of d.Labels.
func (w *Writer) Set(d *Desc, v float64, values ...string) {
	if !w.ok(d, values) || d.Type == Histogram {
		if d.Type == Histogram {
			w.reg.dropped.Add(1)
		}
		return
	}
	w.samples[d] = append(w.samples[d], d.Name+labels(d.Labels, values, "", "")+" "+number(v))
}

// Bool writes 1 or 0.
func (w *Writer) Bool(d *Desc, on bool, values ...string) {
	v := 0.0
	if on {
		v = 1
	}
	w.Set(d, v, values...)
}

// Time writes t as Unix seconds; the zero time writes nothing.
func (w *Writer) Time(d *Desc, t time.Time, values ...string) {
	if !t.IsZero() {
		w.Set(d, float64(t.UnixMilli())/1000, values...)
	}
}

// Histogram writes h, a histogram d declares.
func (w *Writer) Histogram(d *Desc, h *Hist, values ...string) {
	if !w.ok(d, values) || d.Type != Histogram || !slices.Equal(h.bounds, d.Buckets) {
		if w.reg.known[d] {
			w.reg.dropped.Add(1)
		}
		return
	}
	counts, sum, n := h.snapshot()
	var cum uint64
	for i, b := range h.bounds {
		cum += counts[i]
		w.samples[d] = append(w.samples[d], d.Name+"_bucket"+labels(d.Labels, values, "le", number(b))+" "+strconv.FormatUint(cum, 10))
	}
	w.samples[d] = append(w.samples[d],
		d.Name+"_bucket"+labels(d.Labels, values, "le", "+Inf")+" "+strconv.FormatUint(n, 10),
		d.Name+"_sum"+labels(d.Labels, values, "", "")+" "+number(sum),
		d.Name+"_count"+labels(d.Labels, values, "", "")+" "+strconv.FormatUint(n, 10))
}

func (w *Writer) ok(d *Desc, values []string) bool {
	if !w.reg.known[d] || len(values) != len(d.Labels) {
		w.reg.dropped.Add(1)
		return false
	}
	for i, v := range values {
		if !validValue(d.Labels[i], v) {
			w.reg.dropped.Add(1)
			return false
		}
	}
	return true
}

// Write runs every collector and writes their samples in Prometheus's text
// format, metric by metric in the allowlist's order, each metric's samples
// sorted.
func (r *Registry) Write(ctx context.Context) []byte {
	w := &Writer{reg: r, samples: map[*Desc][]string{}}
	for _, c := range r.collectors {
		c.Collect(ctx, w)
	}
	w.Set(droppedDesc, float64(r.dropped.Load()))
	var b bytes.Buffer
	for _, d := range r.descs {
		s := w.samples[d]
		if len(s) == 0 {
			continue
		}
		if d.Type != Histogram {
			slices.Sort(s)
		}
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", d.Name, d.Help, d.Name, d.Type)
		for _, line := range s {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// Handler serves /metrics to a request with the bearer token, and 401 to
// any other. With no token, it answers 404: /metrics isn't there (ADR
// 0040).
func (r *Registry) Handler(token string) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if token == "" {
			http.NotFound(w, req)
			return
		}
		if subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "the metrics token is needed", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 8*time.Second)
		defer cancel()
		body := r.Write(ctx)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write(body)
	})
}

// Hist is a histogram's buckets, safe for use at once by many.
type Hist struct {
	bounds []float64
	mu     sync.Mutex
	counts []uint64
	sum    float64
	n      uint64
}

// NewHist returns an empty histogram with d's buckets.
func NewHist(d *Desc) *Hist {
	return &Hist{bounds: d.Buckets, counts: make([]uint64, len(d.Buckets))}
}

// Observe adds one value.
func (h *Hist) Observe(v float64) {
	if math.IsNaN(v) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n++
	h.sum += v
	for i, b := range h.bounds {
		if v <= b {
			h.counts[i]++
			return
		}
	}
}

func (h *Hist) snapshot() ([]uint64, float64, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.counts), h.sum, h.n
}

func validName(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return s != ""
}

// validValue says whether v may be key's value: a number for an ID, a name
// an admin gave a recorder or trunk-recorder gave a system (printable, at
// most 100 bytes), a version, or else a fixed word in lower case.
func validValue(key, v string) bool {
	switch key {
	case "system", "talkgroup":
		_, err := strconv.ParseInt(v, 10, 64)
		return err == nil
	case "recorder", "other_recorder", "short_name":
		if v == "" || len(v) > 100 || !utf8.ValidString(v) {
			return false
		}
		for _, c := range v {
			if !unicode.IsPrint(c) {
				return false
			}
		}
		return true
	case "version":
		return v != "" && len(v) <= 64 && strings.Trim(v, "0123456789.-+abcdefghijklmnopqrstuvwxyz") == ""
	default:
		return v != "" && len(v) <= 64 && strings.Trim(v, "abcdefghijklmnopqrstuvwxyz0123456789_") == ""
	}
}

// labels writes {k="v",...}, with an extra label (le) last when given.
func labels(keys, values []string, extraKey, extraValue string) string {
	if len(keys) == 0 && extraKey == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(escape(values[i]))
		b.WriteByte('"')
	}
	if extraKey != "" {
		if len(keys) > 0 {
			b.WriteByte(',')
		}
		b.WriteString(extraKey + `="` + extraValue + `"`)
	}
	b.WriteByte('}')
	return b.String()
}

var escaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escape(s string) string { return escaper.Replace(s) }

func number(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
