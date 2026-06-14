package physics

import (
	"math"
	"testing"
)

// emptyCol is open space: nothing is ever solid.
func emptyCol() *Collision {
	return &Collision{Solid: func(tx, ty int) bool { return false }}
}

// floorCol returns a collision with a solid floor: every tile at or below
// tile row floorTY is solid, everything above is open.
func floorCol(floorTY int) *Collision {
	return &Collision{Solid: func(tx, ty int) bool { return ty >= floorTY }}
}

func approx(t *testing.T, name string, got, want, tol float32) {
	t.Helper()
	if d := float32(math.Abs(float64(got - want))); d > tol {
		t.Errorf("%s = %v, want %v (±%v), off by %v", name, got, want, tol, d)
	}
}

// standOn places a core so its feet rest on the floor at tile row floorTY,
// i.e. the ground probe (Pos.Y + PhysicalSize/2 + 5) lands in the floor.
func standOn(col *Collision, floorTY int) *Core {
	floorTopWorld := float32(floorTY * TileSize)
	// Pos.Y + 14 + 5 == floorTopWorld -> grounded probe just reaches floor.
	return NewCore(col, Vec2{X: 100, Y: floorTopWorld - (PhysicalSize/2 + 5)})
}

func TestGravityInAir(t *testing.T) {
	c := NewCore(emptyCol(), Vec2{X: 100, Y: 100})
	c.Tick(Input{})
	approx(t, "VelY after 1 tick", c.Vel.Y, 0.5, 1e-4)

	// Velocity keeps accumulating gravity each tick (no terminal velocity
	// until the 6000 clamp, far away).
	c2 := NewCore(emptyCol(), Vec2{X: 100, Y: 100})
	for range 10 {
		c2.Tick(Input{})
		c2.Move()
	}
	approx(t, "VelY after 10 ticks", c2.Vel.Y, 5.0, 1e-3)
}

func TestGroundAcceleratesToMaxSpeed(t *testing.T) {
	c := standOn(floorCol(10), 10)
	if !c.Grounded() {
		t.Fatalf("expected tee to be grounded, pos=%v", c.Pos)
	}
	// Accel 2.0/tick from 0 reaches the 10.0 ground cap in 5 ticks.
	for i := range 5 {
		c.Tick(Input{Direction: 1})
		approx(t, "VelX", c.Vel.X, float32(2*(i+1)), 1e-4)
		c.Move()
	}
	// Further ticks stay clamped at the ground control speed.
	c.Tick(Input{Direction: 1})
	approx(t, "VelX clamped", c.Vel.X, 10.0, 1e-4)
}

func TestGroundFrictionDecays(t *testing.T) {
	c := standOn(floorCol(10), 10)
	c.Vel.X = 8
	// Direction 0 on the ground multiplies VelX by GroundFriction (0.5).
	c.Tick(Input{Direction: 0})
	approx(t, "VelX after friction", c.Vel.X, 4.0, 1e-4)
}

func TestGroundJumpImpulse(t *testing.T) {
	c := standOn(floorCol(10), 10)
	c.Tick(Input{Jump: true})
	approx(t, "VelY ground jump", c.Vel.Y, -DefaultTuning().GroundJumpImpulse, 1e-4)
	if c.Jumped&1 == 0 {
		t.Errorf("expected in-progress jump bit set, jumped=%b", c.Jumped)
	}
}

func TestHeldJumpDoesNotRetrigger(t *testing.T) {
	c := standOn(floorCol(10), 10)
	c.Tick(Input{Jump: true})
	first := c.Vel.Y
	// Hold jump again while still rising and off the ground: no new impulse,
	// only gravity should change VelY.
	c.Move()
	before := c.Vel.Y
	c.Tick(Input{Jump: true})
	approx(t, "VelY held jump (gravity only)", c.Vel.Y, before+0.5, 1e-4)
	if c.Vel.Y <= first {
		// sanity: VelY became less negative (gravity), not another -13.2 impulse
		t.Errorf("held jump appears to have retriggered: first=%v now=%v", first, c.Vel.Y)
	}
}

func TestAirJumpRequiresRelease(t *testing.T) {
	c := standOn(floorCol(10), 10)
	// Ground jump.
	c.Tick(Input{Jump: true})
	c.Move()
	// Release.
	c.Tick(Input{Jump: false})
	c.Move()
	// Air jump (now off ground, jump pressed again).
	if c.Grounded() {
		t.Fatalf("expected airborne before air jump")
	}
	c.Tick(Input{Jump: true})
	approx(t, "VelY air jump", c.Vel.Y, -DefaultTuning().AirJumpImpulse, 1e-3)
	if c.Jumped&2 == 0 {
		t.Errorf("expected air-jump bit set after air jump, jumped=%b", c.Jumped)
	}
}

func TestWallStopsHorizontalVelocity(t *testing.T) {
	// Floor at row 10, wall at column 4 (world x >= 128).
	col := &Collision{Solid: func(tx, ty int) bool { return ty >= 10 || tx >= 4 }}
	c := NewCore(col, Vec2{X: 80, Y: float32(10*TileSize) - (PhysicalSize/2 + 5)})
	c.Vel.X = 10
	// Drive right into the wall for several ticks.
	hit := false
	for range 30 {
		c.Step(Input{Direction: 1})
		if c.Vel.X == 0 {
			hit = true
			break
		}
	}
	if !hit {
		t.Fatalf("expected horizontal velocity to be zeroed at wall, VelX=%v pos=%v", c.Vel.X, c.Pos)
	}
	// The tee box (half width 14) must not penetrate the wall at x=128.
	if c.Pos.X+PhysicalSize/2 > 128+1 {
		t.Errorf("tee penetrated wall: right edge=%v", c.Pos.X+PhysicalSize/2)
	}
}

func TestFallComesToRestOnFloor(t *testing.T) {
	col := floorCol(10)
	// Start above the floor.
	c := NewCore(col, Vec2{X: 100, Y: float32(10*TileSize) - 200})
	for range 200 {
		c.Step(Input{})
	}
	if c.Vel.Y != 0 {
		t.Errorf("expected to rest on floor with VelY=0, got %v", c.Vel.Y)
	}
	if !c.Grounded() {
		t.Errorf("expected to be grounded after landing, pos=%v", c.Pos)
	}
	// Should rest just above the floor surface (floor top at y=320).
	if c.Pos.Y > 320 {
		t.Errorf("tee sank into floor: y=%v", c.Pos.Y)
	}
}

func TestDeterministic(t *testing.T) {
	run := func() (x, y int) {
		c := standOn(floorCol(10), 10)
		in := Input{Direction: 1, Jump: true, TargetX: 256}
		for i := range 50 {
			// Toggle jump to allow edges.
			in.Jump = i%4 == 0
			c.Step(in)
		}
		return c.QuantizedPos()
	}
	x1, y1 := run()
	x2, y2 := run()
	if x1 != x2 || y1 != y2 {
		t.Errorf("simulation not deterministic: (%d,%d) vs (%d,%d)", x1, y1, x2, y2)
	}
}
