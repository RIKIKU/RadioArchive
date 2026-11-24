package public

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/RIKIKU/RadioArchive/internal/gate"
)

// The live feed (SPEC.md R36, R37, R75, R76; ADR 0007): one WebSocket per
// listener, no sign-in. The listener sends its listening set, and may send
// it again at any time; the gate sends what of the live calls it may have,
// filtered by the set. Everything a listener receives comes from the gate's
// live feed, which has already decided it.

// LivePath is the listener's WebSocket.
const LivePath = "/api/v1/live"

// Limits on what a listener sends.
const (
	// MaxLiveMessage is the longest message a listener may send.
	MaxLiveMessage = 64 << 10
	// MaxListeningSet is the most talkgroups one set may name.
	MaxListeningSet = 5000
	// liveWriteTimeout closes a listener that takes this long to take one
	// message.
	liveWriteTimeout = 10 * time.Second
	// livePing keeps idle sockets open through proxies.
	livePing = 30 * time.Second
)

// TypeSet is the listener's message naming its listening set.
const TypeSet = "set"

// SetMessage is the listener's listening set (ADR 0007):
//
//	{"type": "set", "talkgroups": [{"id": 1001}, {"system": 2, "id": 1001}], "last_event_id": 1764000000123456}
//
// {"id": T}, or the bare number T, is that talkgroup ID in any system (with
// one system, simply that talkgroup); {"system": S, "id": T} names one
// system's, as the catalogue does when there are several (R15). The
// browser sends objects (web/src/lib/live/listening-set.ts). last_event_id is read only in
// the first set: the events after it, from the last 5 minutes, are sent
// first (R37).
type SetMessage struct {
	Type        string            `json:"type"`
	Talkgroups  []json.RawMessage `json:"talkgroups"`
	LastEventID *int64            `json:"last_event_id"`
}

type setEntry struct {
	System *int64 `json:"system"`
	ID     *int32 `json:"id"`
}

// ParseSet reads a set message's talkgroups.
func ParseSet(m SetMessage) (gate.ListeningSet, error) {
	if len(m.Talkgroups) > MaxListeningSet {
		return gate.ListeningSet{}, fmt.Errorf("at most %d talkgroups", MaxListeningSet)
	}
	set := gate.ListeningSet{Any: []int32{}, InSystem: map[int64][]int32{}}
	for _, raw := range m.Talkgroups {
		var n int32
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return gate.ListeningSet{}, errors.New("a talkgroup can't be null")
		}
		if err := json.Unmarshal(raw, &n); err == nil {
			set.Any = append(set.Any, n)
			continue
		}
		var e setEntry
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil || e.ID == nil {
			return gate.ListeningSet{}, errors.New(`each talkgroup is an ID, {"id": ID} or {"system": ID, "id": ID}`)
		}
		if e.System == nil {
			set.Any = append(set.Any, *e.ID)
			continue
		}
		set.InSystem[*e.System] = append(set.InSystem[*e.System], *e.ID)
	}
	return set, nil
}

func (a *API) live(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return // Accept has answered
	}
	defer conn.CloseNow()
	conn.SetReadLimit(MaxLiveMessage)
	sub := a.g.Live().Subscribe()
	defer sub.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// The reader takes the listener's sets. A bad message closes the
	// socket, as a broken client's would be.
	closing := make(chan closeReason, 1)
	go func() { closing <- readSets(ctx, conn, sub) }()
	go func() {
		t := time.NewTicker(livePing)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, liveWriteTimeout)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	if write(ctx, conn, websocket.MessageText, sub.Welcome()) != nil {
		return
	}
	for {
		select {
		case m := <-sub.Messages():
			typ := websocket.MessageText
			if m.Binary {
				typ = websocket.MessageBinary
			}
			if write(ctx, conn, typ, m.Data) != nil {
				return
			}
		case <-sub.Done():
			_ = conn.Close(websocket.StatusTryAgainLater, "too far behind; reconnect with last_event_id")
			return
		case c := <-closing:
			if c.code != 0 {
				_ = conn.Close(c.code, c.reason)
			}
			return
		case <-ctx.Done():
			return
		}
	}
}

func write(ctx context.Context, conn *websocket.Conn, typ websocket.MessageType, b []byte) error {
	ctx, cancel := context.WithTimeout(ctx, liveWriteTimeout)
	defer cancel()
	return conn.Write(ctx, typ, b)
}

type closeReason struct {
	code   websocket.StatusCode
	reason string
}

// readSets reads the listener's messages until the socket ends, and says
// how to close it for a bad one.
func readSets(ctx context.Context, conn *websocket.Conn, sub *gate.Subscription) closeReason {
	for {
		typ, b, err := conn.Read(ctx)
		if err != nil {
			return closeReason{}
		}
		if typ != websocket.MessageText {
			return closeReason{websocket.StatusUnsupportedData, "only text messages are taken"}
		}
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(b, &head) != nil {
			return closeReason{websocket.StatusInvalidFramePayloadData, "a message must be a JSON object"}
		}
		if head.Type != TypeSet {
			continue // a newer client's: ignored
		}
		var m SetMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return closeReason{websocket.StatusPolicyViolation, "set: invalid"}
		}
		set, err := ParseSet(m)
		if err != nil {
			return closeReason{websocket.StatusPolicyViolation, "set: " + err.Error()}
		}
		sub.Listen(set, m.LastEventID)
	}
}
