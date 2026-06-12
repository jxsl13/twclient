package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
)

// openCollision is empty space (nothing solid), for deterministic motion tests.
func openCollision() *physics.Collision {
	return &physics.Collision{Solid: func(int, int) bool { return false }}
}

// V9: re-simulating buffered inputs from the acked snapshot reproduces exactly
// the state a directly-driven core reaches (deterministic re-sim).
func TestPredictedWorldReSimDeterministic(t *testing.T) {
	col := openCollision()
	tun := physics.DefaultTuning()
	base := 100
	seed := CharacterState{X: 1000, Y: 1000}

	var buf predInputBuffer
	in := packet.PlayerInput{Direction: packet.DirRight, Hook: packet.HookOff}
	for tick := base + 1; tick <= base+5; tick++ {
		buf.record(tick, in)
	}

	w := newPredictedWorld(col, tun, base, map[int]CharacterState{1: seed})
	w.advanceOwn(1, base+5, &buf)
	got, ok := w.character(1)
	if !ok {
		t.Fatal("no predicted character")
	}

	// Reference: drive a fresh core the same way.
	ref := seedCore(col, tun, seed)
	for i := 0; i < 5; i++ {
		ref.Step(inputToPhysics(in))
	}
	rx, ry := ref.QuantizedPos()
	if got.X != rx || got.Y != ry {
		t.Errorf("re-sim mismatch: predicted (%d,%d) vs reference (%d,%d)", got.X, got.Y, rx, ry)
	}

	// Moving right must have advanced X past the seed.
	if got.X <= seed.X {
		t.Errorf("expected rightward movement, X=%d seed=%d", got.X, seed.X)
	}
}

// V9: with predTick == baseTick there is nothing to re-sim; the predicted
// state equals the seed.
func TestPredictedWorldNoAdvance(t *testing.T) {
	col := openCollision()
	seed := CharacterState{X: 500, Y: 700}
	w := newPredictedWorld(col, physics.DefaultTuning(), 50, map[int]CharacterState{2: seed})

	var buf predInputBuffer
	w.advanceOwn(2, 50, &buf) // predTick == baseTick

	got, _ := w.character(2)
	if got.X != seed.X || got.Y != seed.Y {
		t.Errorf("no-advance should equal seed: got (%d,%d) want (%d,%d)", got.X, got.Y, seed.X, seed.Y)
	}
}

// V9: re-sim stops at the first missing input rather than guessing.
func TestPredictedWorldStopsAtGap(t *testing.T) {
	col := openCollision()
	base := 10
	var buf predInputBuffer
	in := packet.PlayerInput{Direction: packet.DirRight}
	buf.record(base+1, in)
	buf.record(base+2, in)
	// tick base+3 intentionally missing

	w := newPredictedWorld(col, physics.DefaultTuning(), base, map[int]CharacterState{0: {X: 0, Y: 0}})
	w.advanceOwn(0, base+5, &buf)
	got, _ := w.character(0)

	ref := seedCore(col, physics.DefaultTuning(), CharacterState{X: 0, Y: 0})
	ref.Step(inputToPhysics(in))
	ref.Step(inputToPhysics(in))
	rx, _ := ref.QuantizedPos()
	if got.X != rx {
		t.Errorf("re-sim should stop at gap (2 steps): got X=%d want %d", got.X, rx)
	}
}
