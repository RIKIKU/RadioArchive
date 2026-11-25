package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// The monitoring files (SPEC.md R91; ADR 0042), checked without a
// monitoring stack: CI's monitoring job loads them into PagerArchive's.

type ruleFile struct {
	APIVersion int `yaml:"apiVersion"`
	Groups     []struct {
		Name   string `yaml:"name"`
		Folder string `yaml:"folder"`
		Rules  []struct {
			UID       string `yaml:"uid"`
			Title     string `yaml:"title"`
			Condition string `yaml:"condition"`
			For       string `yaml:"for"`
			Data      []struct {
				RefID string `yaml:"refId"`
				Model struct {
					Expr       string `yaml:"expr"`
					Type       string `yaml:"type"`
					Conditions []struct {
						Evaluator struct {
							Params []float64 `yaml:"params"`
						} `yaml:"evaluator"`
					} `yaml:"conditions"`
				} `yaml:"model"`
			} `yaml:"data"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

var metricName = regexp.MustCompile(`radioarchive_[a-z0-9_]+`)

// exported says whether a name in a query is one the API exports.
func exported(name string) bool {
	for _, d := range telemetry.All {
		if name == d.Name || (d.Type == telemetry.Histogram && slices.Contains(
			[]string{d.Name + "_bucket", d.Name + "_sum", d.Name + "_count"}, name)) {
			return true
		}
	}
	return false
}

func checkQuery(t *testing.T, where, expr string) {
	t.Helper()
	if !strings.Contains(expr, `job="radioarchive"`) {
		t.Errorf("%s: the query doesn't say job=\"radioarchive\", so another app's series could match: %s", where, expr)
	}
	for _, n := range metricName.FindAllString(expr, -1) {
		if !exported(n) {
			t.Errorf("%s: %s isn't a metric the API exports (internal/telemetry/names.go)", where, n)
		}
	}
}

// TestAlertRules: R91's rules, each with its threshold in params, in a
// folder and group of their own, with UIDs of their own, querying only
// metrics the API exports.
func TestAlertRules(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("monitoring", "alerts.d", "radioarchive.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var f ruleFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.APIVersion != 1 || len(f.Groups) != 1 || f.Groups[0].Folder != "RadioArchive" || f.Groups[0].Name != "RadioArchive" {
		t.Fatalf("want one group RadioArchive in folder RadioArchive: %+v", f.Groups)
	}
	want := []string{"ra-recorder-disconnected", "ra-recorder-deaf", "ra-recorder-quiet", "ra-unattached-calls",
		"ra-recording-missing", "ra-live-delay", "ra-recordings-disk", "ra-retention-shortened",
		"ra-recordings-volume-full", "ra-client-queue", "ra-api-unreachable", "ra-backup-failing", "ra-radio-full", "ra-upgrade-failed"}
	var got []string
	for _, r := range f.Groups[0].Rules {
		got = append(got, r.UID)
		params := 0
		for _, d := range r.Data {
			if d.Model.Expr != "" {
				checkQuery(t, r.UID, d.Model.Expr)
			}
			if d.RefID == r.Condition && d.Model.Type == "threshold" {
				for _, c := range d.Model.Conditions {
					params += len(c.Evaluator.Params)
				}
			}
		}
		if params != 1 {
			t.Errorf("%s: its condition has %d params, want its threshold as one", r.UID, params)
		}
		if r.Labels["app"] != "radioarchive" || r.Annotations["summary"] == "" || r.Annotations["description"] == "" {
			t.Errorf("%s: labels %v, annotations %v", r.UID, r.Labels, r.Annotations)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("rules %v, want R91's %v", got, want)
	}
	// R91's times.
	fors := map[string]string{}
	for _, r := range f.Groups[0].Rules {
		fors[r.UID] = r.For
	}
	for uid, d := range map[string]string{"ra-recorder-disconnected": "2m", "ra-recorder-deaf": "2m", "ra-live-delay": "5m", "ra-radio-full": "10m"} {
		if fors[uid] != d {
			t.Errorf("%s waits %q, want %s (R91)", uid, fors[uid], d)
		}
	}
}

// TestDashboards: each dashboard has a UID of its own and the radioarchive
// tag, and every panel queries only metrics the API exports.
func TestDashboards(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("monitoring", "dashboards.d", "RadioArchive", "*.json"))
	if err != nil || len(files) < 3 {
		t.Fatalf("dashboards: %v %v", files, err)
	}
	uids := map[string]bool{}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			UID    string   `json:"uid"`
			Title  string   `json:"title"`
			Tags   []string `json:"tags"`
			Panels []struct {
				ID      int    `json:"id"`
				Title   string `json:"title"`
				Targets []struct {
					Expr string `json:"expr"`
				} `json:"targets"`
			} `json:"panels"`
		}
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if !strings.HasPrefix(d.UID, "ra-") || uids[d.UID] || !slices.Contains(d.Tags, "radioarchive") {
			t.Errorf("%s: uid %q, tags %v", file, d.UID, d.Tags)
		}
		uids[d.UID] = true
		ids := map[int]bool{}
		for _, p := range d.Panels {
			if ids[p.ID] || len(p.Targets) == 0 {
				t.Errorf("%s: panel %d %q", file, p.ID, p.Title)
			}
			ids[p.ID] = true
			for _, tg := range p.Targets {
				checkQuery(t, d.UID+"/"+p.Title, tg.Expr)
			}
		}
	}
}

// TestAlloySnippet scrapes the API's alias with the token file beside it.
func TestAlloySnippet(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("monitoring", "alloy.d", "radioarchive.alloy"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"radioarchive:8080"`, `"/etc/alloy/include/radioarchive-metrics-token"`, `job_name         = "radioarchive"`,
		"prometheus.remote_write.prometheus.receiver", `metrics_path     = "/metrics"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("radioarchive.alloy has no %s", want)
		}
	}
}
