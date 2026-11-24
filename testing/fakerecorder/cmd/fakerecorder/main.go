// Command fakerecorder replays a scenario to a recorder client over the
// recorder plugin's UDP protocol (ADR 0002), in place of trunk-recorder.
//
//	go run ./testing/fakerecorder/cmd/fakerecorder -scenario FILE [-addr 127.0.0.1:9850] [-fixtures DIR] [-speed 1]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/RIKIKU/RadioArchive/testing/fakerecorder"
)

func main() {
	scenario := flag.String("scenario", "", "scenario JSON file (required)")
	addr := flag.String("addr", "127.0.0.1:9850", "recorder client's UDP address (loopback only)")
	fixtures := flag.String("fixtures", filepath.Join("testing", "fakerecorder", "testdata", "fixtures"), "directory of fixture PCM files")
	speed := flag.Float64("speed", 1, "1 keeps the scenario's timing, 10 is ten times faster, 0 sends at once")
	flag.Parse()
	if err := run(*scenario, *addr, *fixtures, *speed); err != nil {
		fmt.Fprintln(os.Stderr, "fakerecorder:", err)
		os.Exit(1)
	}
}

func run(scenarioPath, addr, fixtures string, speed float64) error {
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
	conn, err := fakerecorder.Dial(addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := fakerecorder.Replay(ctx, conn, plan, speed); err != nil {
		return err
	}
	fmt.Printf("sent %d datagrams to %s\n", len(plan), addr)
	return nil
}
