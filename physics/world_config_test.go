package physics

import "testing"

// freezeCol is open space where every tile freezes the tee.
func freezeCol() *Collision {
	return &Collision{
		Solid:  func(tx, ty int) bool { return false },
		Freeze: func(tx, ty int) bool { return true },
	}
}

// TestFreezePredictedOnlyOnDDRace verifies that a freeze tile suppresses tee
// control only when WorldConfig.PredictFreeze is set (DDRace). With the default
// vanilla config the same tile is inert, so movement is unaffected (V10b).
func TestFreezePredictedOnlyOnDDRace(t *testing.T) {
	in := Input{Direction: 1}

	vanilla := NewCore(freezeCol(), Vec2{X: 100, Y: 100})
	vanilla.Tick(in)
	if vanilla.Vel.X <= 0 {
		t.Fatalf("vanilla core on freeze tile should still accelerate: Vel.X=%v", vanilla.Vel.X)
	}

	ddrace := NewCore(freezeCol(), Vec2{X: 100, Y: 100})
	ddrace.SetWorldConfig(DDRaceWorldConfig())
	if !ddrace.Frozen() {
		t.Fatal("ddrace core on freeze tile should report Frozen")
	}
	ddrace.Tick(in)
	if ddrace.Vel.X != 0 {
		t.Fatalf("frozen core must not accept movement input: Vel.X=%v", ddrace.Vel.X)
	}
}

// TestFreezeReleasesHook verifies a frozen tee cannot start (or hold) a hook.
func TestFreezeReleasesHook(t *testing.T) {
	c := NewCore(freezeCol(), Vec2{X: 100, Y: 100})
	c.SetWorldConfig(DDRaceWorldConfig())
	c.Tick(Input{Hook: true, TargetX: 1})
	if c.HookState != HookIdle {
		t.Fatalf("frozen core should not launch hook: HookState=%d", c.HookState)
	}
}

// TestWeaponsGatedByConfig verifies grenade firing (rocket-jump impulse) is
// only simulated when WorldConfig.PredictWeapons is set. A grenade fired into
// the floor explodes and pushes the tee upward; with weapons off, only gravity
// acts.
func TestWeaponsGatedByConfig(t *testing.T) {
	fire := Input{FireGrenade: true, TargetX: 0, TargetY: 1}

	run := func(cfg WorldConfig) float32 {
		c := NewCore(floorCol(5), Vec2{X: 100, Y: float32(5*TileSize) - 30})
		c.SetWorldConfig(cfg)
		c.Tick(fire)
		for i := 0; i < 6; i++ {
			c.Tick(Input{})
		}
		return c.Vel.Y
	}

	off := WorldConfig{IsVanilla: true} // PredictWeapons false
	if vy := run(off); vy <= 0 {
		t.Fatalf("with weapons off, only gravity should act (Vel.Y>0): got %v", vy)
	}

	on := DefaultWorldConfig() // PredictWeapons true
	if vy := run(on); vy >= 0 {
		t.Fatalf("with weapons on, floor explosion should push tee up (Vel.Y<0): got %v", vy)
	}
}
