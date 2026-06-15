package net7

import (
	"log/slog"
	"net"
	"testing"

	"github.com/jxsl13/twclient/network"
	"github.com/jxsl13/twclient/packet"
)

// newTestSessionLive adds a snap storage, a discard logger, and a UDP conn dialed
// to a LOCAL SINK so every Send* / ackSnap transmits without error (V133).
func newTestSessionLive(t *testing.T) *Session {
	t.Helper()
	s := newTestSession()
	s.log = slog.New(slog.DiscardHandler)
	s.reader.snaps = packet.NewSnapStorage(SnapItemSize)

	sink, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, e := sink.ReadFrom(buf); e != nil {
				return
			}
		}
	}()

	conn, err := network.Dial(sink.LocalAddr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s.conn = conn
	return s
}

// The 0.7 snapshot-reception handlers: tick/delta framing → SnapStorage →
// EventSnapshot + ack (V133).
func TestProcessSnap07(t *testing.T) {
	s := newTestSessionLive(t)

	s.processSnapEmpty(packInts(10, -1))
	if e, ok := recv(t, s).(packet.EventSnapshot); !ok || e.Snap == nil {
		t.Fatalf("snap-empty: %#v", e)
	}

	s.processSnapSingle(packInts(20, -1, 0, 0))
	if e, ok := recv(t, s).(packet.EventSnapshot); !ok || e.Snap == nil {
		t.Fatalf("snap-single: %#v", e)
	}

	// Multi-part: single-part fast path, then a 2-part assembly.
	s.processSnapMulti(packInts(30, -1, 1, 0, 0, 0))
	if _, ok := recv(t, s).(packet.EventSnapshot); !ok {
		t.Error("snap-multi(1): no snapshot")
	}
	s.processSnapMulti(packInts(32, -1, 2, 0, 0, 0))
	s.processSnapMulti(packInts(32, -1, 2, 1, 0, 0))
	if _, ok := recv(t, s).(packet.EventSnapshot); !ok {
		t.Error("snap-multi(2): no snapshot after assembly")
	}

	// Truncated → no event, no panic.
	s.processSnapSingle(packInts(99))
	select {
	case ev := <-s.reader.eventCh:
		t.Errorf("truncated snap emitted: %#v", ev)
	default:
	}
}
