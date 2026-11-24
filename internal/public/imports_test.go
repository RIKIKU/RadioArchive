package public

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The gate is the public API's only way to the deployment's data (SPEC.md
// R32, ADR 0004): its code may import this repo's gate and nothing else of
// the repo but the download file names (internal/filename, ADR 0014), which
// read nothing and only name what the gate gave, no third-party module (the database driver is one) but the
// WebSocket library the live feed speaks through (ADR 0007), which reaches
// no data, and none of the standard library's ways to a database, a file or
// a process.
var (
	allowedRepo = map[string]bool{"github.com/RIKIKU/RadioArchive/internal/gate": true,
		"github.com/RIKIKU/RadioArchive/internal/filename": true}
	allowedMods = map[string]bool{"github.com/coder/websocket": true}
	deniedStd   = []string{"os", "io/fs", "io/ioutil", "path/filepath", "database/sql", "syscall",
		"unsafe", "plugin", "net/http/httputil", "embed", "runtime/cgo", "reflect"}
)

func importAllowed(path string) bool {
	if strings.HasPrefix(path, "github.com/RIKIKU/RadioArchive/") {
		return allowedRepo[path]
	}
	first, _, _ := strings.Cut(path, "/")
	if strings.Contains(first, ".") {
		return allowedMods[path] // a third-party module: only the WebSocket library
	}
	for _, d := range deniedStd {
		if path == d || strings.HasPrefix(path, d+"/") {
			return false
		}
	}
	return true
}

func TestImportsOnlyTheGate(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			if !importAllowed(p) {
				t.Errorf("%s imports %s: the public API reads only through internal/gate", f, p)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no files checked")
	}
}

// The rule itself: what it must refuse.
func TestImportRule(t *testing.T) {
	for _, p := range []string{"os", "io/fs", "database/sql", "path/filepath",
		"github.com/RIKIKU/RadioArchive/internal/store", "github.com/RIKIKU/RadioArchive/internal/store/db",
		"github.com/RIKIKU/RadioArchive/internal/recordings", "github.com/RIKIKU/RadioArchive/internal/server",
		"github.com/jackc/pgx/v5", "github.com/jackc/pgx/v5/pgxpool", "github.com/coder/websocket/wsjson",
		"github.com/RIKIKU/RadioArchive/internal/clientproto"} {
		if importAllowed(p) {
			t.Errorf("%s is allowed", p)
		}
	}
	for _, p := range []string{"net/http", "encoding/json", "github.com/RIKIKU/RadioArchive/internal/gate",
		"github.com/RIKIKU/RadioArchive/internal/filename", "github.com/coder/websocket"} {
		if !importAllowed(p) {
			t.Errorf("%s is refused", p)
		}
	}
}
