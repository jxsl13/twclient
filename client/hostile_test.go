package client

import "testing"

// V70: a nil option and out-of-range config values must not panic at
// construction — nil is ignored, sizes clamp to their defaults/floors.
func TestHostileConstructionNoPanic(t *testing.T) {
	c := New("",
		nil, // ignored
		WithSnapStorageSize(-5),
		WithPredInputRingSize(-1),
		WithEventChanSize(-1),
		WithReadBufferSize(-1),
	)
	if c == nil {
		t.Fatal("New returned nil")
	}
	// Prediction ring is sized + safe even with a negative request.
	if got := len(c.predInputs.ring); got != DefaultPredInputRingSize {
		t.Errorf("predInputs ring = %d, want default %d", got, DefaultPredInputRingSize)
	}
}
