package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
)

// V10: with no inputs to re-apply, the predicted state converges exactly to the
// authoritative snapshot it was seeded from.
func TestPredictionConvergesToSnap(t *testing.T) {
	col := openCollision()
	snap := CharacterState{X: 1234, Y: 5678, VelX: 0, VelY: 0}

	w := newPredictedWorld(col, physics.DefaultTuning(), 100, map[int]CharacterState{1: snap})
	var buf predInputBuffer
	w.advanceOwn(1, 110, &buf) // no inputs buffered -> no re-sim

	got, _ := w.character(1)
	if got.X != snap.X || got.Y != snap.Y {
		t.Errorf("predicted should converge to snap: got (%d,%d) want (%d,%d)",
			got.X, got.Y, snap.X, snap.Y)
	}
}

// V9: reconstructing the world from authoritative state each "snapshot" never
// accumulates error — a re-seed from the same snapshot yields the same result
// regardless of any prior predicted world.
func TestPredictionDriftFreeAcrossReseeds(t *testing.T) {
	col := openCollision()
	tun := physics.DefaultTuning()
	base := 200
	snap := CharacterState{X: 1000, Y: 1000}

	var buf predInputBuffer
	in := packet.PlayerInput{Direction: packet.DirRight}
	for tick := base + 1; tick <= base+8; tick++ {
		buf.record(tick, in)
	}

	// First reconcile cycle.
	w1 := newPredictedWorld(col, tun, base, map[int]CharacterState{1: snap})
	w1.advanceOwn(1, base+8, &buf)
	got1, _ := w1.character(1)

	// Second cycle, re-seeded from the SAME authoritative snapshot. Because the
	// world always starts from snapshot state, the outcome is identical — no
	// drift carried from w1.
	w2 := newPredictedWorld(col, tun, base, map[int]CharacterState{1: snap})
	w2.advanceOwn(1, base+8, &buf)
	got2, _ := w2.character(1)

	if got1 != got2 {
		t.Errorf("re-seed must be deterministic and drift-free: %#v vs %#v", got1, got2)
	}
}

// V9a: extrapolated others are bounded — over a short window a standing player
// stays within a small distance of its snapshot position (no runaway).
func TestExtrapolationBounded(t *testing.T) {
	col := openCollision()
	seed := CharacterState{X: 5000, Y: 5000, Direction: 0}
	w := newPredictedWorld(col, physics.DefaultTuning(), 0, map[int]CharacterState{2: seed})
	w.advanceOthers(1, 10) // 10 ticks of extrapolation, player not moving

	got, _ := w.character(2)
	dx := got.X - seed.X
	if dx < -2 || dx > 2 {
		t.Errorf("standing player drifted too far horizontally: dx=%d", dx)
	}
}
