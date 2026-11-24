package recorderclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/version"
)

// Reconnect backoff (ADR 0002: 1 s to 60 s with jitter).
const (
	backoffMin = time.Second
	backoffMax = time.Minute
)

type outMsg struct {
	typ websocket.MessageType
	b   []byte
}

// liveConn keeps the live WebSocket open, reconnecting with backoff, and
// sends what the engine gives it while it's up.
type liveConn struct {
	url string // ws:// or wss://
	key string
	log *slog.Logger
	m   *Metrics

	out       chan outMsg
	connected chan struct{} // a signal for the engine after each welcome
	up        atomic.Bool
}

func newLiveConn(url, key string, log *slog.Logger, m *Metrics) *liveConn {
	return &liveConn{url: url, key: key, log: log, m: m,
		out: make(chan outMsg, 2048), connected: make(chan struct{}, 1)}
}

func (l *liveConn) send(m outMsg) {
	if !l.up.Load() {
		l.m.Inc(MetricLiveDropped, "reason", "disconnected")
		return
	}
	select {
	case l.out <- m:
	default:
		l.m.Inc(MetricLiveDropped, "reason", "backlog")
	}
}

// SendText implements Live.
func (l *liveConn) SendText(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		l.log.Error("can't encode a live event", "error", err)
		return
	}
	l.send(outMsg{websocket.MessageText, b})
}

// SendFrame implements Live.
func (l *liveConn) SendFrame(b []byte) { l.send(outMsg{websocket.MessageBinary, b}) }

func headers(key string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+key)
	h.Set(cp.HeaderClientVersion, version.Version)
	h.Set(cp.HeaderProtocol, strconv.Itoa(cp.Protocol))
	return h
}

func (l *liveConn) run(ctx context.Context) {
	backoff := backoffMin
	for ctx.Err() == nil {
		welcomed, err := l.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if welcomed {
			backoff = backoffMin
		}
		wait := backoff/2 + rand.N(backoff/2+1) // jitter: half to all of it
		l.log.Warn("live connection to the server closed; reconnecting", "error", err, "in", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(2*backoff, backoffMax)
	}
}

// session runs one connection until it fails.
func (l *liveConn) session(ctx context.Context) (welcomed bool, err error) {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, resp, err := websocket.Dial(dctx, l.url, &websocket.DialOptions{HTTPHeader: headers(l.key)})
	cancel()
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			return false, fmt.Errorf("server refused the connection: %s %s", resp.Status, body)
		}
		return false, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(cp.MaxTextMessage)

	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	typ, b, err := conn.Read(wctx)
	cancel()
	if err != nil {
		return false, err
	}
	var w cp.Welcome
	if typ != websocket.MessageText || json.Unmarshal(b, &w) != nil || w.Type != cp.TypeWelcome {
		return false, errors.New("the server's first message wasn't a welcome")
	}
	l.log.Info("live connection open", "server_version", w.ServerVersion, "protocol", w.Protocol)

	// Anything queued for an earlier socket is stale.
	for len(l.out) > 0 {
		<-l.out
	}
	l.up.Store(true)
	defer l.up.Store(false)
	select {
	case l.connected <- struct{}{}:
	default:
	}

	rerr := make(chan error, 1)
	go func() {
		for {
			typ, b, err := conn.Read(ctx)
			if err != nil {
				rerr <- err
				return
			}
			// The server sends only welcome today; anything else is
			// ignored and counted (ADR 0002).
			var m struct{ Type string }
			if typ != websocket.MessageText || json.Unmarshal(b, &m) != nil || m.Type != cp.TypeWelcome {
				l.m.Inc(MetricLiveDropped, "reason", "server_message")
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusGoingAway, "client stopping")
			return true, ctx.Err()
		case err := <-rerr:
			return true, err
		case m := <-l.out:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, m.typ, m.b)
			cancel()
			if err != nil {
				return true, err
			}
		}
	}
}
