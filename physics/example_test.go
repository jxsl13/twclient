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
