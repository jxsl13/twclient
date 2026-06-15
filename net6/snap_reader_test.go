package net6

import (
	"log/slog"
	"net"
	"testing"

	"github.com/jxsl13/twclient/network"
	"github.com/jxsl13/twclient/packet"
)

// newTestSessionLive adds the state the send/snap handlers need: a snap storage,
// a discard logger, and a UDP conn dialed to a LOCAL SINK (a drained listener) so
// every Send* / ackSnap actually transmits without error — a dead port would
// return ICMP "connection refused" on the connected socket.
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

// processSnapEmpty / processSnapSingle decode the 50Hz snapshot stream (business
// logic: tick/delta framing → SnapStorage.ProcessSnap → EventSnapshot + ack).
func TestProcessSnapEmptyAndSingle(t *testing.T) {
	s := newTestSessionLive(t)

	// Empty snapshot, delta from the empty base (deltaTick -1) → empty snap event.
	s.processSnapEmpty(packInts(10, -1))
	if e, ok := recv(t, s).(packet.EventSnapshot); !ok || e.Snap == nil {
		t.Fatalf("snap-empty did not emit a snapshot: %#v", e)
	}
	if got := s.lastAckedSnap.Load(); got != 10 {
		t.Errorf("snap-empty ack tick = %d, want 10", got)
	}

	// Single-part snapshot with a zero-length part (tick, deltaTick, crc, partSize).
	s.processSnapSingle(packInts(20, -1, 0, 0))
	if e, ok := recv(t, s).(packet.EventSnapshot); !ok || e.Snap == nil {
		t.Fatalf("snap-single did not emit a snapshot: %#v", e)
	}

	// Truncated single snap (missing fields) → no event, no panic.
	s.processSnapSingle(packInts(21))
	select {
	case ev := <-s.reader.eventCh:
		t.Errorf("truncated snap-single produced an event: %#v", ev)
	default:
	}
}
