package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// V54/V41: WithPredInputRingSize sizes the ring; unset = default (256),
// invalid clamps (n<=0 -> default, 0<n<min -> min).
func TestWithPredInputRingSize(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want int
	}{
		{"default", nil, defaultPredInputRingSize},
		{"set", []Option{WithPredInputRingSize(512)}, 512},
		{"clamp-up", []Option{WithPredInputRingSize(1)}, minPredInputRingSize},
		{"clamp-default", []Option{WithPredInputRingSize(0)}, defaultPredInputRingSize},
		{"clamp-default-neg", []Option{WithPredInputRingSize(-5)}, defaultPredInputRingSize},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := New("localhost:8303", c.opts...)
			if got := len(cl.predInputs.ring); got != c.want {
				t.Errorf("ring len = %d, want %d", got, c.want)
			}
		})
	}
}

// V54: record/get round-trip works at a small configured ring, and wraparound
// evicts the stale tick — behavior identical to the default ring, just smaller.
func TestPredInputRingSmallWraparound(t *testing.T) {
	cl := New("localhost:8303", WithPredInputRingSize(minPredInputRingSize))
	size := len(cl.predInputs.ring)
	if size != minPredInputRingSize {
		t.Fatalf("ring len = %d, want %d", size, minPredInputRingSize)
	}

	in := packet.PlayerInput{Direction: 1}
	cl.predInputs.record(10, in)
	if got, ok := cl.predInputs.get(10); !ok || got.Direction != 1 {
		t.Fatalf("round-trip failed: %#v ok=%v", got, ok)
	}
	// Same ring slot, +size ticks later → old tick evicted.
	cl.predInputs.record(10+size, packet.PlayerInput{Direction: -1})
	if _, ok := cl.predInputs.get(10); ok {
		t.Error("stale tick should be evicted after wraparound")
	}
}
