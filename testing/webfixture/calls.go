package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/oggopus"
	"github.com/RIKIKU/RadioArchive/internal/recordings"
	"github.com/jackc/pgx/v5"
)

// fixtureCall is one made-up call for the Recent calls page's tests.
type fixtureCall struct {
	talkgroup int32
	patched   []int32
	start     time.Time
	length    time.Duration
	units     []int64
	// inProgress: no end and no recording yet.
	inProgress bool
}

// The archive the Recent calls page's tests search (web/tests/recent.spec.ts),
// by the hour before now (from), none of it real:
//
//   - 1001 Dispatch North: 60 calls, 7 minutes apart from 2 hours ago back,
//     units 2001-2004 in turn, 2010 too on every fifth;
//   - 1002 Fireground 41: 8 calls, 50 minutes apart, unit 3001, and one in
//     progress since 20 minutes ago, unit 3002;
//   - 1003 Admin (hidden): 3 calls, unit 9003 heard on them alone; a 1001
//     call patched with 1003, unit 9004; and a 1004 (ignored) call, unit
//     9005. None of these may show;
//   - A17's call: 1001, 26 hours ago at 7 s past the minute, unit 2099
//     heard on it alone, to download.
func fixtureCalls(from time.Time) []fixtureCall {
	now := from.Truncate(time.Minute)
	base := now.Add(-2 * time.Hour)
	var cs []fixtureCall
	for i := range 60 {
		units := []int64{2001 + int64(i%4)}
		if i%5 == 0 {
			units = append(units, 2010)
		}
		cs = append(cs, fixtureCall{talkgroup: 1001, start: base.Add(-time.Duration(i)*7*time.Minute + 13*time.Second),
			length: time.Duration(4+i%9) * time.Second, units: units})
	}
	for i := range 8 {
		cs = append(cs, fixtureCall{talkgroup: 1002, start: base.Add(-3*time.Minute - time.Duration(i)*50*time.Minute),
			length: 6 * time.Second, units: []int64{3001}})
	}
	cs = append(cs, fixtureCall{talkgroup: 1002, start: now.Add(-20 * time.Minute), units: []int64{3002}, inProgress: true})
	for i := range 3 {
		cs = append(cs, fixtureCall{talkgroup: 1003, start: base.Add(-time.Minute - time.Duration(i)*30*time.Minute),
			length: 5 * time.Second, units: []int64{9003}})
	}
	cs = append(cs,
		fixtureCall{talkgroup: 1001, patched: []int32{1003}, start: base.Add(-5 * time.Minute), length: 5 * time.Second, units: []int64{9004}},
		fixtureCall{talkgroup: 1004, start: base.Add(-6 * time.Minute), length: 5 * time.Second, units: []int64{9005}},
		fixtureCall{talkgroup: 1001, start: now.Add(-26*time.Hour + 7*time.Second), length: 3 * time.Second, units: []int64{2099}},
	)
	return cs
}

// storeCalls stores the fixture's calls as the named recorder's.
func storeCalls(ctx context.Context, conn *pgx.Conn, recorder, dir string, now time.Time) error {
	for i, c := range fixtureCalls(now) {
		if _, err := storeCall(ctx, conn, recorder, dir, int64(i), c); err != nil {
			return err
		}
	}
	return nil
}

// storeCall stores one call, and its recording unless it's in progress,
// and returns its ID. n tells the calls apart.
func storeCall(ctx context.Context, conn *pgx.Conn, recorder, dir string, n int64, c fixtureCall) (string, error) {
	var ended *time.Time
	if !c.inProgress {
		e := c.start.Add(c.length)
		ended = &e
	}
	patched := c.patched
	if patched == nil {
		patched = []int32{}
	}
	var id string
	err := conn.QueryRow(ctx, `
INSERT INTO calls (recorder_id, sys_num, call_num, start_time, system_id, short_name, talkgroup, patched_talkgroups, started_at, ended_at)
SELECT id, 0, $2, $3, 1, 'ex', $4, $5, $6, $7 FROM recorders WHERE name = $1
RETURNING id::text`, recorder, 7_900_000_000+n, c.start.Unix(), c.talkgroup, patched, c.start, ended).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", fmt.Errorf("no recorder named %q", recorder)
	}
	if err != nil {
		return "", err
	}
	for j, u := range c.units {
		if _, err := conn.Exec(ctx, `INSERT INTO transmissions (call_id, offset_ms, unit, duration_ms) VALUES ($1, $2, $3, $4)`,
			id, j*1000, u, 1000); err != nil {
			return "", err
		}
	}
	if c.inProgress {
		return id, nil
	}
	audio, err := silence(c.length, uint32(n))
	if err != nil {
		return "", err
	}
	rel := recordings.Path(id)
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256(audio)
	_, err = conn.Exec(ctx, `UPDATE calls SET recording_path = $2, recording_size = $3, recording_sha256 = $4, recording_at = $5 WHERE id = $1`,
		id, rel, len(audio), sum[:], *ended)
	return id, err
}

// silence is an Ogg/Opus file of length's silence: 20 ms packets that are
// a TOC byte alone (CELT, full band, mono, one empty frame), which a
// decoder plays as silence.
func silence(length time.Duration, serial uint32) ([]byte, error) {
	var b bytes.Buffer
	w, err := oggopus.NewWriter(&b, serial+1, 312, 8000)
	if err != nil {
		return nil, err
	}
	frames := int(length / (20 * time.Millisecond))
	for range frames {
		if err := w.WritePacket([]byte{0xF8}, 960); err != nil {
			return nil, err
		}
	}
	if err := w.Close(uint64(frames) * 960); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// storeOne stores one call on a talkgroup from unit, of 3 s ending now, for
// a test that needs a call to end at a known moment (A14), and prints its
// ID and when it ended, in Unix milliseconds.
func storeOne(ctx context.Context, conn *pgx.Conn, recorder, dir string, talkgroup int32, unit int64) error {
	now := time.Now().Truncate(time.Millisecond)
	c := fixtureCall{talkgroup: talkgroup, start: now.Add(-3 * time.Second), length: 3 * time.Second, units: []int64{unit}}
	id, err := storeCall(ctx, conn, recorder, dir, now.UnixMilli()%100, c)
	if err != nil {
		return err
	}
	fmt.Println(id, now.UnixMilli())
	return nil
}

// A16's call (issue #80): a call link's card while the call is in
// progress, once it has ended, and once retention has deleted it.

// startCall stores a call on talkgroup from unit, in progress since 5 s
// ago, with no recording, and prints its ID.
func startCall(ctx context.Context, conn *pgx.Conn, recorder string, talkgroup int32, unit int64) error {
	now := time.Now().Truncate(time.Millisecond)
	c := fixtureCall{talkgroup: talkgroup, start: now.Add(-5 * time.Second), units: []int64{unit}, inProgress: true}
	id, err := storeCall(ctx, conn, recorder, "", 1_000_000+now.UnixMilli()%1_000_000, c)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// endCall ends a call in progress now and stores its recording, as the
// recorder client's upload would.
func endCall(ctx context.Context, conn *pgx.Conn, id, dir string) error {
	var started time.Time
	if err := conn.QueryRow(ctx, `SELECT started_at FROM calls WHERE id = $1::uuid AND ended_at IS NULL`, id).Scan(&started); err != nil {
		return fmt.Errorf("no call %s in progress: %w", id, err)
	}
	ended := time.Now().Truncate(time.Millisecond)
	audio, err := silence(ended.Sub(started), uint32(ended.UnixMilli()))
	if err != nil {
		return err
	}
	rel := recordings.Path(id)
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		return err
	}
	sum := sha256.Sum256(audio)
	_, err = conn.Exec(ctx, `UPDATE calls SET ended_at = $2, recording_path = $3, recording_size = $4, recording_sha256 = $5, recording_at = $2 WHERE id = $1::uuid`,
		id, ended, rel, len(audio), sum[:])
	return err
}

// deleteCall deletes a call as retention does (R88): its row, then its
// recording's file.
func deleteCall(ctx context.Context, conn *pgx.Conn, id, dir string) error {
	var rel *string
	if err := conn.QueryRow(ctx, `DELETE FROM calls WHERE id = $1::uuid RETURNING recording_path`, id).Scan(&rel); err != nil {
		return fmt.Errorf("no call %s: %w", id, err)
	}
	if rel == nil {
		return nil
	}
	return os.Remove(filepath.Join(dir, filepath.FromSlash(*rel)))
}
