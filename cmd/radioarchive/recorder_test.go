package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

var keyRE = regexp.MustCompile(`rak_[A-Za-z0-9_-]{43}`)

func TestIntegrationRecorderCommands(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, storetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	name := fmt.Sprintf("North hill %d", time.Now().UnixNano())
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := recorderCommand(ctx, st, args, &out)
		return out.String(), err
	}

	out, err := run("add", name)
	if err != nil {
		t.Fatal(err)
	}
	key := keyRE.FindString(out)
	if key == "" {
		t.Fatalf("add printed no key:\n%s", out)
	}
	// Only the key's hash is stored.
	var stored []byte
	if err := st.Pool.QueryRow(ctx, `SELECT key_hash FROM recorders WHERE name = $1`, name).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, store.HashClientKey(key)) || bytes.Contains(stored, []byte(key)) {
		t.Error("the stored hash isn't the key's SHA-256")
	}
	if _, err := st.AuthenticateRecorder(ctx, key); err != nil {
		t.Errorf("new key: %v", err)
	}
	if _, err := run("add", name); err != store.ErrRecorderExists {
		t.Errorf("adding twice: %v", err)
	}

	out, err = run("rotate-key", name)
	newKey := keyRE.FindString(out)
	if err != nil || newKey == "" || newKey == key {
		t.Fatalf("rotate-key: %v\n%s", err, out)
	}
	if _, err := st.AuthenticateRecorder(ctx, key); err != store.ErrUnknownKey {
		t.Errorf("old key after rotation: %v", err)
	}
	if _, err := st.AuthenticateRecorder(ctx, newKey); err != nil {
		t.Errorf("new key after rotation: %v", err)
	}

	out, err = run("list")
	if err != nil || !regexp.MustCompile(regexp.QuoteMeta(name)+` +active`).MatchString(out) || strings.Contains(out, "rak_") {
		t.Errorf("list: %v\n%s", err, out)
	}
	if _, err := run("retire", name); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AuthenticateRecorder(ctx, newKey); err != store.ErrUnknownKey {
		t.Errorf("key after retiring: %v", err)
	}
	if out, _ := run("list"); !regexp.MustCompile(regexp.QuoteMeta(name) + ` +retired `).MatchString(out) {
		t.Errorf("list after retiring:\n%s", out)
	}
	if _, err := run("rotate-key", name); err != store.ErrNoActiveRecorder {
		t.Errorf("rotating a retired recorder: %v", err)
	}
	for _, args := range [][]string{{}, {"add"}, {"add", "a", "b"}, {"frobnicate"}, {"add", " padded"}} {
		if _, err := run(args...); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}
