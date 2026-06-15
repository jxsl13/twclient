package udpfault

import (
	"net"
	"sync"
	"testing"
	"time"
)

// sink is a UDP server that records the payloads it receives (the proxy target).
type sink struct {
	conn *net.UDPConn
	mu   sync.Mutex
	got  [][]byte
}

func newSink(t *testing.T) *sink {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{conn: c}
	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			s.mu.Lock()
			s.got = append(s.got, append([]byte(nil), buf[:n]...))
			s.mu.Unlock()
		}
	}()
	return s
}

func (s *sink) addr() string { return s.conn.LocalAddr().String() }
func (s *sink) count() int   { s.mu.Lock(); defer s.mu.Unlock(); return len(s.got) }
func (s *sink) close()       { s.conn.Close() }

// sendN dials addr and sends n distinct datagrams, then waits briefly for the
// proxy to forward them.
func sendN(t *testing.T, addr string, n int) {
	t.Helper()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := range n {
		if _, err := c.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // pace so UDP ordering holds locally
	}
	time.Sleep(100 * time.Millisecond)
}

// TestPassThrough: a zero policy forwards every datagram byte-for-byte.
func TestPassThrough(t *testing.T) {
	s := newSink(t)
	defer s.close()
	p, err := New(s.addr(), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	const n = 20
	sendN(t, p.Addr(), n)
	if got := s.count(); got != n {
		t.Fatalf("pass-through delivered %d/%d datagrams", got, n)
	}
}

// TestDrop: a seeded 50% drop policy delivers fewer than all, deterministically.
func TestDrop(t *testing.T) {
	s := newSink(t)
	defer s.close()
	p, err := New(s.addr(), Policy{DropC2S: 0.5, Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	const n = 40
	sendN(t, p.Addr(), n)
	got := s.count()
	if got == 0 || got >= n {
		t.Fatalf("drop policy delivered %d/%d (want some loss, not all/none)", got, n)
	}
	if int(p.Dropped()) != n-got {
		t.Fatalf("dropped accounting: Dropped()=%d, n-got=%d", p.Dropped(), n-got)
	}
	t.Logf("seed 42 drop 0.5: delivered %d/%d (dropped %d)", got, n, p.Dropped())
}

// TestDuplicate: a 100% dup policy delivers each datagram twice.
func TestDuplicate(t *testing.T) {
	s := newSink(t)
	defer s.close()
	p, err := New(s.addr(), Policy{DupC2S: 1.0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	const n = 10
	sendN(t, p.Addr(), n)
	if got := s.count(); got != 2*n {
		t.Fatalf("dup policy delivered %d, want %d (each twice)", got, 2*n)
	}
}

// TestCloseNoLeak: Close stops the goroutines (wg.Wait returns) even with a
// latency policy in flight. Run under -race for the leak/ordering check.
func TestCloseNoLeak(t *testing.T) {
	s := newSink(t)
	defer s.close()
	p, err := New(s.addr(), Policy{LatencyC2S: 50 * time.Millisecond, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	sendN(t, p.Addr(), 5)
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return (goroutine leak)")
	}
}
