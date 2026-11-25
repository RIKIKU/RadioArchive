package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var testStart = time.Unix(1764000000, 0)

// TestAllowlist: every declared metric has its name, type, help and label
// keys from the allowlist (NewRegistry panics otherwise), and a Desc with a
// key that isn't on it can't be registered.
func TestAllowlist(t *testing.T) {
	NewRegistry(All)
	for _, d := range []*Desc{
		{Name: "radioarchive_x", Type: Gauge, Help: "x", Labels: []string{"talkgroup_name"}},
		{Name: "radioarchive_x", Type: Gauge, Help: "x", Labels: []string{"address"}},
		{Name: "other_x", Type: Gauge, Help: "x"},
		{Name: "radioarchive_x", Type: Gauge},
		{Name: "radioarchive_x", Type: Histogram, Help: "x"},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%+v was registered", d)
				}
			}()
			NewRegistry([]*Desc{d})
		}()
	}
	// No label key could name a talkgroup or a unit, or hold an address.
	for _, k := range LabelKeys {
		if strings.Contains(k, "name") && k != "short_name" || strings.Contains(k, "unit") ||
			strings.Contains(k, "addr") || strings.Contains(k, "ip") {
			t.Errorf("label key %q", k)
		}
	}
}

// TestWriterChecksValues: a sample is written only for a declared metric,
// with its labels, each an ID, code or fixed word; anything else is
// dropped and counted.
func TestWriterChecksValues(t *testing.T) {
	g := &Desc{Name: "radioarchive_g", Type: Gauge, Help: "A gauge.", Labels: []string{"talkgroup", "reason"}}
	rec := &Desc{Name: "radioarchive_r", Type: Gauge, Help: "By recorder.", Labels: []string{"recorder"}}
	undeclared := &Desc{Name: "radioarchive_u", Type: Gauge, Help: "Not registered."}
	r := NewRegistry([]*Desc{g, rec})
	r.Add(CollectorFunc(func(_ context.Context, w *Writer) {
		w.Set(g, 3, "1001", "not_attached")        // kept
		w.Set(g, 1, "Dispatch North", "ok")        // a name as an ID: dropped
		w.Set(g, 1, "1002", "Fireground 41")       // a name as a code: dropped
		w.Set(g, 1, "1003")                        // too few labels: dropped
		w.Set(undeclared, 1)                       // not declared: dropped
		w.Set(rec, 1, "North \"hill\"")            // a recorder's name: kept, escaped
		w.Set(rec, 1, "bad\x00name")               // a control character: dropped
		w.Set(rec, 1, strings.Repeat("x", 101))    // too long: dropped
		w.Set(g, 2, "203.0.113.7", "not_attached") // an address isn't an ID: dropped
	}))
	out := string(r.Write(context.Background()))
	want := "# HELP radioarchive_g A gauge.\n# TYPE radioarchive_g gauge\n" +
		`radioarchive_g{talkgroup="1001",reason="not_attached"} 3` + "\n" +
		"# HELP radioarchive_r By recorder.\n# TYPE radioarchive_r gauge\n" +
		`radioarchive_r{recorder="North \"hill\""} 1` + "\n" +
		"# HELP radioarchive_telemetry_dropped_total " + droppedDesc.Help + "\n# TYPE radioarchive_telemetry_dropped_total counter\n" +
		"radioarchive_telemetry_dropped_total 7\n"
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestHistogram(t *testing.T) {
	d := &Desc{Name: "radioarchive_h_seconds", Type: Histogram, Help: "H.", Buckets: []float64{0.5, 1, 2}}
	r := NewRegistry([]*Desc{d})
	h := NewHist(d)
	for _, v := range []float64{0.2, 0.7, 0.9, 1.5, 9} {
		h.Observe(v)
	}
	r.Add(CollectorFunc(func(_ context.Context, w *Writer) { w.Histogram(d, h) }))
	out := string(r.Write(context.Background()))
	for _, want := range []string{
		`radioarchive_h_seconds_bucket{le="0.5"} 1`, `radioarchive_h_seconds_bucket{le="1"} 3`,
		`radioarchive_h_seconds_bucket{le="2"} 4`, `radioarchive_h_seconds_bucket{le="+Inf"} 5`,
		"radioarchive_h_seconds_sum 12.3", "radioarchive_h_seconds_count 5",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

// TestHandler: /metrics with the token, 401 without it, 404 with no token
// set (ADR 0040).
func TestHandler(t *testing.T) {
	r := NewRegistry(All)
	r.Add(Process("1.2.3", testStart))
	for _, c := range []struct {
		token, auth string
		code        int
	}{
		{"", "", 404},
		{"", "Bearer ", 404},
		{"made-up-token-0123456789", "", 401},
		{"made-up-token-0123456789", "Bearer wrong", 401},
		{"made-up-token-0123456789", "made-up-token-0123456789", 401},
		{"made-up-token-0123456789", "Bearer made-up-token-0123456789", 200},
	} {
		req := httptest.NewRequest("GET", "/metrics", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		rec := httptest.NewRecorder()
		r.Handler(c.token).ServeHTTP(rec, req)
		if rec.Code != c.code {
			t.Errorf("token %q, Authorization %q: %d, want %d", c.token, c.auth, rec.Code, c.code)
		}
		if c.code == http.StatusOK {
			body := rec.Body.String()
			if !strings.Contains(body, `radioarchive_build_info{version="1.2.3"} 1`) ||
				!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
				t.Errorf("metrics: %s %s", rec.Header().Get("Content-Type"), body)
			}
			// Prometheus's text format: every sample line is name{labels} value.
			line := regexp.MustCompile(`^(# (HELP|TYPE) .+|radioarchive_[a-z0-9_]+(\{[a-z_]+="[^"]*"(,[a-z_]+="[^"]*")*\})? \S+)$`)
			for _, l := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
				if !line.MatchString(l) {
					t.Errorf("not Prometheus text: %q", l)
				}
			}
		}
	}
}

// TestNamesAreUnique and documented in names.go's order.
func TestNamesAreUnique(t *testing.T) {
	var names []string
	for _, d := range All {
		if slices.Contains(names, d.Name) {
			t.Errorf("%s twice", d.Name)
		}
		names = append(names, d.Name)
	}
}
