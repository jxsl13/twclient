package network

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// udpPeer is a local UDP server for transport tests: it records what it receives
// and can echo a canned reply.
type udpPeer struct {
	conn  *net.UDPConn
	recvd atomic.Int32
}

func newPeer(t *testing.T, reply []byte) *udpPeer {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	p := &udpPeer{conn: c}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			p.recvd.Add(1)
			if reply != nil {
				_, _ = c.WriteToUDP(reply, from)
			}
			_ = n
		}
	}()
	t.Cleanup(func() { c.Close() })
	return p
}

func (p *udpPeer) addr() string { return p.conn.LocalAddr().String() }

// TestDialSendRecv covers Dial, SendRaw, RecvContext + Close against a peer that
// echoes.
func TestDialSendRecv(t *testing.T) {
	peer := newPeer(t, []byte("pong"))
	c, err := Dial(peer.addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.SendRaw([]byte("ping")); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	got, err := c.RecvContext(ctx)
	if err != nil {
		t.Fatalf("RecvContext: %v", err)
	}
	if string(got) != "pong" {
		t.Fatalf("recv = %q, want pong", got)
	}
}

// TestRecvContextCancel covers the ctx-cancel path of RecvContext (no reply).
func TestRecvContextCancel(t *testing.T) {
	peer := newPeer(t, nil) // never replies
	c, err := Dial(peer.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if _, err := c.RecvContext(ctx); err == nil {
		t.Fatal("RecvContext returned nil error with no reply + cancelled ctx")
	}
}

// TestRecvResending covers RecvResending: the peer ignores the first datagram,
// the resend callback fires on the interval, and a later reply completes it.
func TestRecvResending(t *testing.T) {
	// Peer replies only once it has seen >=2 datagrams (i.e. after a resend).
	c2, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	go func() {
		buf := make([]byte, 2048)
		seen := 0
		for {
			n, from, err := c2.ReadFromUDP(buf)
			if err != nil {
				return
			}
			seen++
			if seen >= 2 {
				_, _ = c2.WriteToUDP([]byte("ok"), from)
			}
			_ = n
		}
	}()

	c, err := Dial(c2.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var resends atomic.Int32
	send := func() error { resends.Add(1); return c.SendRaw([]byte("req")) }
	if err := send(); err != nil { // initial
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	got, err := c.RecvResending(ctx, 100*time.Millisecond, send)
	if err != nil {
		t.Fatalf("RecvResending: %v (resends=%d)", err, resends.Load())
	}
	if string(got) != "ok" {
		t.Fatalf("recv = %q, want ok", got)
	}
	if resends.Load() < 2 {
		t.Fatalf("expected at least one resend, got %d sends", resends.Load())
	}
}

// TestDialBadAddress covers the Dial error path.
func TestDialBadAddress(t *testing.T) {
	if _, err := Dial("not-a-valid-address"); err == nil {
		t.Fatal("Dial succeeded on a bad address")
	}
}

// TestOptionsAndAccessors covers DialOption application + the accessors.
func TestOptionsAndAccessors(t *testing.T) {
	peer := newPeer(t, nil)
	c, err := Dial(peer.addr(),
		WithReadTimeout(3*time.Second),
		WithReadBufferSize(256*1024),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.ReadTimeout() != 3*time.Second {
		t.Errorf("ReadTimeout = %v, want 3s", c.ReadTimeout())
	}
	if c.Log() == nil {
		t.Error("Log() is nil")
	}
	// Double close must not panic.
	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("first Close: %v", err)
	}
	_ = c.Close()
}
