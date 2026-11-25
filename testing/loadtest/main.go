// Command loadtest is A37's load (SPEC.md section 7, A37; issue #155): it
// holds listener WebSockets open on the live feed, and writes a scenario
// for the fake recorder with calls at once, back to back, for a while.
//
//	go run ./testing/loadtest scenario -out FILE [-calls 5] [-length 20s] [-for 1h] [-media DIR]
//	go run ./testing/loadtest listen -url http://127.0.0.1:8080 [-sockets 50] [-for 1h]
//
// scenario: every length, calls start at once on talkgroups 1001 and 1002
// from units 5001 onwards, each its length of fixture tone, ending with the
// WAV trunk-recorder would write under DIR, for the fake recorder
// (testing/fakerecorder) to replay at speed 1. listen: sockets listeners
// each with {1001, 1002}, reading everything, until the time is up or
// interrupted, printing each minute how many are open and the frames read.
// Only for the made-up fixture; it sends nothing a listener wouldn't.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loadtest scenario|listen [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "scenario":
		err = scenario(os.Args[2:])
	case "listen":
		err = listen(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

func scenario(args []string) error {
	fs := flag.NewFlagSet("scenario", flag.ExitOnError)
	out := fs.String("out", "", "scenario file to write")
	calls := fs.Int("calls", 5, "calls at once")
	length := fs.Duration("length", 20*time.Second, "each call's length")
	total := fs.Duration("for", time.Hour, "how long the calls go on")
	media := fs.String("media", "/app/media", "where the WAVs go, as the client sees them")
	_ = fs.Parse(args)
	if *out == "" {
		return fmt.Errorf("-out is required")
	}
	epoch := time.Now().Add(5 * time.Second).UnixMilli()
	type call struct {
		SysNum    int    `json:"sys_num"`
		CallNum   int64  `json:"call_num"`
		StartTime int64  `json:"start_time"`
		ShortName string `json:"short_name"`
		Talkgroup int    `json:"talkgroup"`
		Freq      int64  `json:"freq"`
	}
	s := map[string]any{"boot_id": epoch % 1_000_000_000, "epoch_ms": epoch}
	cs := map[string]call{}
	events := []map[string]any{}
	secs := int(length.Seconds())
	num := epoch * 10
	for round := 0; time.Duration(round)*(*length) < *total; round++ {
		at := int64(round) * length.Milliseconds()
		if round%2 == 0 {
			events = append(events, map[string]any{"at_ms": at, "type": "heartbeat",
				"systems": []map[string]any{{"sys_num": 0, "short_name": "ex", "system_type": "p25", "decode_rate": 40}}})
		}
		for i := range *calls {
			num++
			name := fmt.Sprintf("r%d-%d", round, i)
			tg := 1001 + i%2
			cs[name] = call{CallNum: num, StartTime: (epoch + at) / 1000, ShortName: "ex", Talkgroup: tg, Freq: 851012500 + int64(i)*12500}
			unit := 5001 + i
			events = append(events, map[string]any{"at_ms": at, "type": "transmission", "call": name, "unit": unit})
			for k := range secs - 1 {
				events = append(events, map[string]any{"at_ms": at + int64(k)*1000, "type": "audio", "call": name, "unit": unit, "pcm": "tone-440hz-1s.pcm"})
			}
			events = append(events, map[string]any{"at_ms": at + int64(secs-1)*1000 + 100, "type": "call_end", "call": name,
				"filename": fmt.Sprintf("%s/ex/load-%d.wav", strings.TrimSuffix(*media, "/"), num)})
		}
	}
	s["calls"] = cs
	s["events"] = events
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(*out, b, 0o644)
}

func listen(args []string) error {
	fs := flag.NewFlagSet("listen", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "the radio's base URL")
	sockets := fs.Int("sockets", 50, "listener sockets to hold open")
	total := fs.Duration("for", time.Hour, "how long")
	_ = fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *total)
	defer cancel()
	ws := "ws" + strings.TrimPrefix(*url, "http") + "/api/v1/live"
	var open, frames, events atomic.Int64
	var wg sync.WaitGroup
	for i := range *sockets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				c, _, err := websocket.Dial(ctx, ws, nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "socket %d: %v\n", i, err)
					time.Sleep(5 * time.Second)
					continue
				}
				c.SetReadLimit(1 << 20)
				open.Add(1)
				_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"set","talkgroups":[{"id":1001},{"id":1002}]}`))
				for {
					typ, _, err := c.Read(ctx)
					if err != nil {
						break
					}
					if typ == websocket.MessageBinary {
						frames.Add(1)
					} else {
						events.Add(1)
					}
				}
				open.Add(-1)
				c.CloseNow()
			}
		}()
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			fmt.Printf("%s done: frames %d, events %d\n", time.Now().UTC().Format(time.TimeOnly), frames.Load(), events.Load())
			return nil
		case <-t.C:
			fmt.Printf("%s open %d, frames %d, events %d\n", time.Now().UTC().Format(time.TimeOnly), open.Load(), frames.Load(), events.Load())
		}
	}
}
