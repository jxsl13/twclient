package client

import "testing"

// V70: a nil option and out-of-range config values must not panic at
// construction — nil is ignored, sizes clamp to their defaults/floors.
func TestHostileConstructionNoPanic(t *testing.T) {
	c := New("",
		nil, // ignored
		WithSnapStorageSize(-5),
		WithPredInputRingSize(-1),
		WithInputTimingRingSize(-1),
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
	// Input-timing ring likewise falls back to its default (V54).
	if got := len(c.predTime.inputs); got != DefaultInputTimingRingSize {
		t.Errorf("predTime ring = %d, want default %d", got, DefaultInputTimingRingSize)
	}
}

// V53/V54: WithInputTimingRingSize sizes the CSmoothTime input-timing ring;
// unset keeps the default, an explicit value is honored, a too-small value is
// clamped up to the floor, and a negative value falls back to the default.
func TestInputTimingRingSize(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want int
	}{
		{"default-unset", nil, DefaultInputTimingRingSize},
		{"explicit", []Option{WithInputTimingRingSize(512)}, 512},
		{"clamp-floor", []Option{WithInputTimingRingSize(1)}, MinInputTimingRingSize},
		{"negative-default", []Option{WithInputTimingRingSize(-3)}, DefaultInputTimingRingSize},
		{"zero-default", []Option{WithInputTimingRingSize(0)}, DefaultInputTimingRingSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New("", tc.opts...)
			if got := len(c.predTime.inputs); got != tc.want {
				t.Errorf("ring len = %d, want %d", got, tc.want)
			}
			// Reset preserves the configured length (V54).
			c.predTime.Reset()
			if got := len(c.predTime.inputs); got != tc.want {
				t.Errorf("ring len after Reset = %d, want %d", got, tc.want)
			}
		})
	}
}
