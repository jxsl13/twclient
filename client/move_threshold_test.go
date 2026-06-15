package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// V127/V41: WithMoveEventThreshold sets the per-Client EventPlayerMove throttle;
// unset or n <= 0 falls back to DefaultMoveEventThreshold (16).
func TestWithMoveEventThreshold(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want int
	}{
		{"default", nil, DefaultMoveEventThreshold},
		{"set", []Option{WithMoveEventThreshold(64)}, 64},
		{"set-one", []Option{WithMoveEventThreshold(1)}, 1},
		{"clamp-zero", []Option{WithMoveEventThreshold(0)}, DefaultMoveEventThreshold},
		{"clamp-neg", []Option{WithMoveEventThreshold(-5)}, DefaultMoveEventThreshold},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New("localhost:8303", tc.opts...)
			if c.moveEventThreshold != tc.want {
				t.Errorf("moveEventThreshold = %d, want %d", c.moveEventThreshold, tc.want)
			}
		})
	}
}

// moveCount runs a 2-snapshot move of dx world units through a SnapStorage with
// the given threshold and returns the number of EventPlayerMove emitted.
func moveCount(t *testing.T, threshold, dx int) int {
	t.Helper()
	var ss SnapStorage
	ss.localCID = 1
	ss.moveEventThreshold = threshold
	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		charItemFull(2, CharacterState{X: 100, Y: 100}),
	}})
	ss.deriveEvents() // enter-sight
	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{
		charItemFull(2, CharacterState{X: 100 + dx, Y: 100}),
	}})
	return countEvents[packet.EventPlayerMove](ss.deriveEvents())
}

// V127: the configured threshold gates EventPlayerMove; a small threshold emits
// on a move a large threshold suppresses. Zero falls back to the default (V48).
func TestMoveEventThresholdGate(t *testing.T) {
	const dx = 50
	if got := moveCount(t, 1, dx); got != 1 {
		t.Errorf("threshold 1, dx %d: want 1 move, got %d", dx, got)
	}
	if got := moveCount(t, 100, dx); got != 0 {
		t.Errorf("threshold 100, dx %d: want 0 moves, got %d", dx, got)
	}
	// Zero-value SnapStorage → DefaultMoveEventThreshold (16): below suppresses,
	// at/above emits, identical to the original const behavior.
	if got := moveCount(t, 0, DefaultMoveEventThreshold-1); got != 0 {
		t.Errorf("default threshold, sub-threshold move: want 0, got %d", got)
	}
	if got := moveCount(t, 0, DefaultMoveEventThreshold); got != 1 {
		t.Errorf("default threshold, at-threshold move: want 1, got %d", got)
	}
}
