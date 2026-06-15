package physics_test

import (
	"fmt"

	"github.com/jxsl13/twclient/physics"
)

// DefaultTuning returns DDNet's default movement constants (src/game/tuning.h /
// CTuningParams). These drive the deterministic character simulation used for
// client-side prediction.
func ExampleDefaultTuning() {
	tun := physics.DefaultTuning()
	fmt.Printf("gravity=%.1f ground_jump=%.1f hook_length=%.0f\n",
		tun.Gravity, tun.GroundJumpImpulse, tun.HookLength)
	// Output: gravity=0.5 ground_jump=13.2 hook_length=380
}

// A short deterministic simulation: a tee dropped above a solid floor falls
// under gravity while running right. Core.Step advances velocity (Tick) and
// position (Move) one server tick at a time — the exact loop client-side
// prediction runs to extrapolate characters forward between snapshots. Collision
// is normally built from a loaded map (NewCollision); here a hand-built floor
// (everything from tile row 12 down is solid) keeps the example self-contained.
func ExampleCore() {
	col := &physics.Collision{
		Solid:       func(_, ty int) bool { return ty >= 12 },
		NoHook:      func(_, _ int) bool { return false },
		Freeze:      func(_, _ int) bool { return false },
		HookThrough: func(_, _ int) bool { return false },
	}
	core := physics.NewCore(col, physics.Vec2{X: 100, Y: 100})

	startX, startY := core.Pos.X, core.Pos.Y
	for range 50 {
		core.Step(physics.Input{Direction: 1}) // hold "move right"
	}

	fmt.Println("fell:", core.Pos.Y > startY)
	fmt.Println("moved right:", core.Pos.X > startX)
	// Output:
	// fell: true
	// moved right: true
}
