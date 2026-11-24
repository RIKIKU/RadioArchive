// Command fakerecorder replays a scenario to a recorder client over the
// recorder plugin's UDP protocol (ADR 0002), in place of trunk-recorder.
//
//	go run ./testing/fakerecorder/cmd/fakerecorder -scenario FILE [-addr 127.0.0.1:9850] [-fixtures DIR] [-recordings DIR] [-speed 1] [-first-pcm]
//
// With -first-pcm it prints a line of JSON as it sends each call's first
// audio datagram, {"call": "<scenario name>", "sent_at_ms": <Unix ms>}:
// where the delay budget (R38, A6) starts.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/RIKIKU/RadioArchive/testing/fakerecorder"
)

func main() {
	scenario := flag.String("scenario", "", "scenario JSON file (required)")
	addr := flag.String("addr", "127.0.0.1:9850", "recorder client's UDP address (loopback only)")
	fixtures := flag.String("fixtures", filepath.Join("testing", "fakerecorder", "testdata", "fixtures"), "directory of fixture PCM files")
	speed := flag.Float64("speed", 1, "1 keeps the scenario's timing, 10 is ten times faster, 0 sends at once")
	recordings := flag.String("recordings", "", "first write the WAV trunk-recorder would have written for each call_end under this directory (a filename /app/media/x.wav lands at DIR/app/media/x.wav)")
	firstPCM := flag.Bool("first-pcm", false, "print a line of JSON as each call's first audio datagram is sent")
	flag.Parse()
	if err := run(*scenario, *addr, *fixtures, *recordings, *speed, *firstPCM); err != nil {
		fmt.Fprintln(os.Stderr, "fakerecorder:", err)
		os.Exit(1)
	}
}

func run(scenarioPath, addr, fixtures, recordings string, speed float64, firstPCM bool) error {
	if scenarioPath == "" {
		return fmt.Errorf("-scenario is required")
	}
	f, err := os.Open(scenarioPath)
	if err != nil {
		return err
	}
	defer f.Close()
	s, err := fakerecorder.LoadScenario(f)
	if err != nil {
		return err
	}
	plan, err := fakerecorder.Plan(s, os.DirFS(fixtures))
	if err != nil {
		return err
	}
	if recordings != "" {
		if err := fakerecorder.WriteRecordings(s, os.DirFS(fixtures), recordings); err != nil {
			return err
		}
	}
	conn, err := fakerecorder.Dial(addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var sent func(fakerecorder.Send, time.Time)
	if firstPCM {
		out := json.NewEncoder(os.Stdout)
		sent = func(d fakerecorder.Send, at time.Time) {
			if d.FirstPCM {
				_ = out.Encode(map[string]any{"call": d.Call, "sent_at_ms": at.UnixMilli()})
			}
		}
	}
	if err := fakerecorder.ReplayFunc(ctx, conn, plan, speed, sent); err != nil {
		return err
	}
	if !firstPCM {
		fmt.Printf("sent %d datagrams to %s\n", len(plan), addr)
	}
	return nil
}
