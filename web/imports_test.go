package web

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The app shell carries no deployment data (ADR 0004): its code imports
// nothing of this repo and no third-party module, so it can't reach the
// store, the gate or the database. It reads only its own embedded files.
func TestImportsNothingOfTheRepo(t *testing.T) {
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
			first, _, _ := strings.Cut(p, "/")
			if strings.Contains(first, ".") || p == "database/sql" || p == "os" || p == "os/exec" {
				t.Errorf("%s imports %s: the app shell serves only its embedded files", f, p)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no files checked")
	}
}
