// Package deploy holds the files the compose stack mounts (Authentik's
// blueprints and email templates, the database's first-start script) and
// the tests that keep compose.yaml honest about them.
package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type service struct {
	Volumes []string          `yaml:"volumes"`
	Labels  map[string]string `yaml:"labels"`
}

type composeFile struct {
	Services map[string]service `yaml:"services"`
}

func readCompose(t *testing.T, file string) composeFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	var c composeFile
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// Compose doesn't notice a mounted file changing, so a service that mounts
// files carries a label holding their hash, and a change to one recreates
// the service on `docker compose up -d`, as PagerArchive's do (ADR 0009).
// The database's first-start script is left out: it means nothing to a
// database that already exists.
//
// Both compose files: the development one, and the release's
// (deploy/release/compose.yaml, ADR 0043), whose ./deploy paths are the
// configuration archive's, unpacked beside it, so the same as the repository's.
func TestChangedConfigRecreatesItsService(t *testing.T) {
	for _, file := range []string{"compose.yaml", "deploy/release/compose.yaml"} {
		t.Run(file, func(t *testing.T) { checkLabels(t, readCompose(t, file)) })
	}
}

func checkLabels(t *testing.T, c composeFile) {
	labelled := 0
	for name, s := range c.Services {
		h := sha256.New()
		files := 0
		for _, v := range s.Volumes {
			src, _, ok := strings.Cut(v, ":")
			if !ok || !strings.HasPrefix(src, "./") || strings.Contains(v, "/docker-entrypoint-initdb.d/") {
				continue
			}
			data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(src)))
			if err != nil {
				t.Fatalf("%s mounts %s: %v", name, src, err)
			}
			sum := sha256.Sum256(data)
			h.Write([]byte(src + " " + hex.EncodeToString(sum[:]) + "\n"))
			files++
		}
		label := s.Labels["radioarchive.config"]
		if files == 0 {
			if label != "" {
				t.Errorf("%s has a config label but mounts no config", name)
			}
			continue
		}
		labelled++
		if want := hex.EncodeToString(h.Sum(nil))[:16]; label != want {
			t.Errorf("%s's mounted files changed, so it wouldn't be recreated: set its radioarchive.config label to %q", name, want)
		}
	}
	if labelled < 2 {
		t.Errorf("only %d services mount config; Authentik's server and worker both do", labelled)
	}
}
