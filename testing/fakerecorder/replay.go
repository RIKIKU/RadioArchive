package fakerecorder

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Dial opens a UDP socket to addr, which must be a loopback address: like the
// recorder plugin, the fake recorder never sends across a network.
func Dial(addr string) (*net.UDPConn, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	if !ua.IP.IsLoopback() {
		return nil, fmt.Errorf("fakerecorder: %s is not a loopback address", addr)
	}
	return net.DialUDP("udp", nil, ua)
}

// Replay sends the plan's datagrams on conn in order. With speed 1 it keeps
// the scenario's timing, with speed 10 it runs ten times faster, and with
// speed 0 it sends as fast as it can.
func Replay(ctx context.Context, conn net.Conn, plan []Send, speed float64) error {
	return ReplayFunc(ctx, conn, plan, speed, nil)
}

// ReplayFunc is Replay, calling sent, if it isn't nil, with each datagram
// and the time it was written.
func ReplayFunc(ctx context.Context, conn net.Conn, plan []Send, speed float64, sent func(Send, time.Time)) error {
	start := time.Now()
	for _, s := range plan {
		if speed > 0 {
			wait := time.Until(start.Add(time.Duration(float64(s.At) / speed)))
			if wait > 0 {
				t := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					t.Stop()
					return ctx.Err()
				case <-t.C:
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := conn.Write(s.Datagram); err != nil {
			return fmt.Errorf("fakerecorder: send: %w", err)
		}
		if sent != nil {
			sent(s, time.Now())
		}
	}
	return nil
}
