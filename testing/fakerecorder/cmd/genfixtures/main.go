// Command genfixtures writes the fake recorder's fixture PCM files.
//
//	go run ./testing/fakerecorder/cmd/genfixtures [dir]
//
// dir defaults to testing/fakerecorder/testdata/fixtures. The output is the
// same on every run; a test fails if the committed files differ from it.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/RIKIKU/RadioArchive/testing/fakerecorder"
)

func main() {
	dir := filepath.Join("testing", "fakerecorder", "testdata", "fixtures")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	files := fakerecorder.Fixtures()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s\t%d bytes\n", path, len(files[name]))
	}
}
