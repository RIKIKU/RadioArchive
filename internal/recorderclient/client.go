// Package recorderclient is the recorder client (SPEC.md R3, ADR 0002). It
// receives the recorder plugin's UDP datagrams on localhost, encodes live
// PCM to Opus and forwards each 20 ms frame at once over the server's live
// WebSocket, uploads each finished recording as Ogg/Opus, relays the
// plugin's heartbeat and drops patched copies (R16).
//
// It's cgo-free itself; the real Opus encoder (internal/opus, cgo and
// libopus) is passed in, so its tests can run with a stand-in.
package recorderclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	rp "github.com/RIKIKU/RadioArchive/internal/recorderproto"
)

// rcvBuf is the UDP receive buffer asked for (ADR 0002).
const rcvBuf = 4 << 20

// Config configures a Client.
type Config struct {
	// ServerURL is the API's base URL, http:// or https://. The live socket
	// uses ws:// or wss:// to the same host.
	ServerURL string
	// ClientKey is this recorder's key, from `radioarchive recorder add`.
	ClientKey string
	// UDPAddr is where the plugin sends, a loopback address (default
	// 127.0.0.1:9850).
	UDPAddr    string
	NewEncoder NewEncoder
	Log        *slog.Logger
	Metrics    *Metrics     // optional
	HTTPClient *http.Client // optional
}

// Client is a running recorder client.
type Client struct {
	cfg  Config
	conn *net.UDPConn
	eng  *engine
	live *liveConn
	up   *memUploader
}

// New checks cfg and binds the UDP socket.
func New(cfg Config) (*Client, error) {
	if cfg.ClientKey == "" {
		return nil, errors.New("the client key is not set")
	}
	if cfg.NewEncoder == nil {
		return nil, errors.New("no Opus encoder")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Metrics == nil {
		cfg.Metrics = NewMetrics()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.UDPAddr == "" {
		cfg.UDPAddr = "127.0.0.1:9850"
	}
	base, err := url.Parse(strings.TrimRight(cfg.ServerURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("server URL %q must be http:// or https://", cfg.ServerURL)
	}
	ws := *base
	ws.Scheme = map[string]string{"http": "ws", "https": "wss"}[base.Scheme]

	ua, err := net.ResolveUDPAddr("udp", cfg.UDPAddr)
	if err != nil {
		return nil, err
	}
	if !ua.IP.IsLoopback() {
		return nil, fmt.Errorf("UDP address %s is not loopback: the plugin only sends on localhost", cfg.UDPAddr)
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	if err := conn.SetReadBuffer(rcvBuf); err != nil {
		cfg.Log.Warn("can't set the UDP receive buffer", "error", err)
	}
	cfg.Log.Info("listening for the recorder plugin", "addr", conn.LocalAddr().String(),
		"receive_buffer", receiveBuffer(conn), "asked", rcvBuf)

	c := &Client{cfg: cfg, conn: conn}
	c.live = newLiveConn(ws.String()+cp.LivePath, cfg.ClientKey, cfg.Log, cfg.Metrics)
	c.up = newMemUploader(base.String()+cp.RecordingsPath, cfg.ClientKey, cfg.HTTPClient, cfg.Log, cfg.Metrics, cfg.NewEncoder)
	c.eng = newEngine(cfg.Log, cfg.Metrics, c.live, c.up, cfg.NewEncoder, time.Now)
	return c, nil
}

// UDPAddr is the bound UDP address.
func (c *Client) UDPAddr() net.Addr { return c.conn.LocalAddr() }

// LiveConnected reports whether the live WebSocket is open (past the
// server's welcome).
func (c *Client) LiveConnected() bool { return c.live.up.Load() }

// Metrics are the client's counters.
func (c *Client) Metrics() *Metrics { return c.cfg.Metrics }

// Run runs the client until ctx ends.
func (c *Client) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.live.run(ctx)
	go c.up.run(ctx)

	dgrams := make(chan []byte, 4096)
	rerr := make(chan error, 1)
	go func() {
		buf := make([]byte, rp.MaxDatagram+1) // one over, so an oversized datagram is seen as such
		for {
			n, _, err := c.conn.ReadFromUDP(buf)
			if err != nil {
				rerr <- err
				return
			}
			select {
			case dgrams <- append([]byte(nil), buf[:n]...):
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		<-ctx.Done()
		c.conn.Close()
	}()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	hb := time.NewTicker(heartbeatTick)
	defer hb.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-rerr:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case d := <-dgrams:
			c.eng.handle(d)
		case <-tick.C:
			c.eng.tick()
		case <-hb.C:
			c.live.SendText(c.eng.heartbeat())
		case <-c.live.connected:
			c.eng.reconnected()
		}
	}
}
