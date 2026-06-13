package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
)

// B2/V9: PredictedProjectiles advances projectiles ballistically to the target
// tick. With prediction disabled it advances to the latest snapshot tick.
func TestPredictedProjectiles(t *testing.T) {
	c := &Client{predTun: physics.DefaultTuning()}
	c.snap.lastTick = 100
	c.snap.projectiles = map[int]packet.ProjectileState{
		1: {ID: 1, X: 0, Y: 0, VelX: 100, VelY: 0, Type: packet.WeaponGun, StartTick: 50},
	}

	got := c.PredictedProjectiles()
	if len(got) != 1 {
		t.Fatalf("want 1 projectile, got %d", len(got))
	}
	// t = (100-50)/50 = 1s, dir = (1,0): x = 2200, y = 1.25/10000*2200^2.
	if got[0].X != 2200 {
		t.Errorf("projectile X: want 2200, got %d", got[0].X)
	}
	if got[0].Y <= 0 {
		t.Errorf("projectile Y should be bent down by gravity, got %d", got[0].Y)
	}
}

// V11: with prediction disabled, predicted accessors equal raw snapshot state.
func TestPredictionDisabledEqualsRaw(t *testing.T) {
	c := &Client{}
	c.snap.localCID = 1
	c.snap.character = CharacterState{X: 42, Y: 7}
	c.snap.characters = map[int]CharacterState{1: {X: 42, Y: 7}, 2: {X: 99}}

	if got := c.PredictedCharacter(); got != c.Character() {
		t.Errorf("disabled PredictedCharacter should equal Character: %#v vs %#v", got, c.Character())
	}
	pc := c.PredictedCharacters()
	if pc[1].X != 42 || pc[2].X != 99 {
		t.Errorf("disabled PredictedCharacters should be raw: %#v", pc)
	}
}

// V10/V11: with prediction enabled the local accessor returns the predicted
// world's state; others stay raw unless antiping is on.
func TestPredictionAccessorsEnabled(t *testing.T) {
	c := &Client{predictEnabled: true}
	c.snap.localCID = 1
	c.snap.character = CharacterState{X: 100}
	c.snap.characters = map[int]CharacterState{1: {X: 100}, 2: {X: 200}}

	// Inject a predicted world with a moved local character.
	col := openCollision()
	c.predWorld = newPredictedWorld(col, physics.DefaultTuning(), 0,
		map[int]CharacterState{1: {X: 150}, 2: {X: 250}})

	if got := c.PredictedCharacter(); got.X != 150 {
		t.Errorf("enabled PredictedCharacter X: want 150, got %d", got.X)
	}
	// Antiping off: others remain raw.
	pc := c.PredictedCharacters()
	if pc[1].X != 150 || pc[2].X != 200 {
		t.Errorf("antiping off: local predicted, others raw; got %#v", pc)
	}
	// Antiping on: others predicted too.
	c.antiping = true
	pc = c.PredictedCharacters()
	if pc[2].X != 250 {
		t.Errorf("antiping on: others predicted; got %#v", pc)
	}
}

// V21: SmoothedCharacters interpolates positions between the previous and
// current predicted worlds (no teleport between ticks).
func TestSmoothedCharacters(t *testing.T) {
	col := openCollision()
	tun := physics.DefaultTuning()
	c := &Client{predictEnabled: true}
	c.prevPredWorld = newPredictedWorld(col, tun, 0, map[int]CharacterState{1: {X: 100, Y: 200}})
	c.predWorld = newPredictedWorld(col, tun, 0, map[int]CharacterState{1: {X: 300, Y: 200}})

	// Midpoint: halfway between 100 and 300.
	mid := c.SmoothedCharacters(0.5)
	if mid[1].X != 200 {
		t.Errorf("intra=0.5 X: want 200, got %d", mid[1].X)
	}
	// Endpoints.
	if c.SmoothedCharacters(0)[1].X != 100 {
		t.Errorf("intra=0 should be previous tick (100)")
	}
	if c.SmoothedCharacters(1)[1].X != 300 {
		t.Errorf("intra=1 should be current tick (300)")
	}
	// Clamping.
	if c.SmoothedCharacters(2)[1].X != 300 {
		t.Errorf("intra>1 should clamp to current")
	}
}

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

// V9a: other players are extrapolated (held direction) while the local player
// is left for the input-driven re-sim.
func TestPredictedWorldExtrapolateOthers(t *testing.T) {
	col := openCollision()
	tun := physics.DefaultTuning()
	base := 100
	chars := map[int]CharacterState{
		1: {X: 1000, Y: 1000, Direction: 1}, // local
		2: {X: 2000, Y: 1000, Direction: 1}, // other, walking right
		3: {X: 3000, Y: 1000, Direction: 0}, // other, standing
	}
	w := newPredictedWorld(col, tun, base, chars)
	w.advanceOthers(1, base+10)

	// Other player 2 walked right; player 3 stayed put (horizontally).
	p2, _ := w.character(2)
	if p2.X <= 2000 {
		t.Errorf("player 2 should have walked right, X=%d", p2.X)
	}
	p3, _ := w.character(3)
	if p3.X != 3000 {
		t.Errorf("standing player 3 should not move horizontally, X=%d", p3.X)
	}
	// Local player 1 must be untouched by advanceOthers.
	p1, _ := w.character(1)
	if p1.X != 1000 {
		t.Errorf("local player must not be extrapolated by advanceOthers, X=%d", p1.X)
	}

	if all := w.characters(); len(all) != 3 {
		t.Errorf("characters() should return all 3, got %d", len(all))
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
