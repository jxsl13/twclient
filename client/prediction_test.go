package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// V9: the prediction input buffer stores and returns inputs keyed by tick.
func TestPredInputBuffer(t *testing.T) {
	var b predInputBuffer

	if _, ok := b.get(10); ok {
		t.Error("empty buffer should report no input")
	}

	in := packet.PlayerInput{Direction: 1, Jump: packet.JumpOn}
	b.record(10, in)
	got, ok := b.get(10)
	if !ok || got.Direction != 1 || got.Jump != packet.JumpOn {
		t.Errorf("record/get round-trip failed: %#v ok=%v", got, ok)
	}

	// A tick that was overwritten by ring wraparound is reported as absent.
	b.record(10+defaultPredInputRingSize, packet.PlayerInput{Direction: -1})
	if _, ok := b.get(10); ok {
		t.Error("stale tick should be evicted after wraparound")
	}
	if got, ok := b.get(10 + defaultPredInputRingSize); !ok || got.Direction != -1 {
		t.Errorf("wrapped slot should hold the newest input: %#v ok=%v", got, ok)
	}
}

// V9: a sequence of ticks round-trips for the re-sim window.
func TestPredInputWindow(t *testing.T) {
	var b predInputBuffer
	for tick := 100; tick < 150; tick++ {
		b.record(tick, packet.PlayerInput{Direction: packet.Direction(tick % 2)})
	}
	for tick := 100; tick < 150; tick++ {
		got, ok := b.get(tick)
		if !ok || got.Direction != packet.Direction(tick%2) {
			t.Fatalf("tick %d missing/wrong: %#v ok=%v", tick, got, ok)
		}
	}
}

// LocalID reports -1 before any snapshot, then the learned id.
func TestLocalID(t *testing.T) {
	c := &Client{}
	if id := c.LocalID(); id != -1 {
		t.Errorf("LocalID before snapshot: want -1, got %d", id)
	}
	c.snap.localCID = 4
	c.snap.processSnapshot(&packet.Snapshot{Tick: 1})
	if id := c.LocalID(); id != 4 {
		t.Errorf("LocalID after snapshot: want 4, got %d", id)
	}
}
