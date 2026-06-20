// Package physics is a faithful Go port of DDNet's character physics
// (CCharacterCore::Tick and CCharacterCore::Move from src/game/gamecore.cpp).
//
// It exists so that reconstructed inputs can be validated and searched
// OFFLINE against a deterministic local simulation before they are sent to
// a live, authoritative server. A DDNet ghost stores sampled character
// state, not raw inputs, so deriving inputs is a constrained inverse-physics
// problem (see docs/GHOST_REPLAY_PROBLEM.md). Without a local simulator that
// problem can only be probed through expensive live-server round-trips; with
// one it becomes a unit-testable, searchable problem.
//
// Fidelity notes:
//   - All state and arithmetic use float32 to mirror C++ `float` (vec2).
//   - The tick order, edge-triggered jump bitmask, hook drag, velocity ramp
//     and MoveBox collision resolution follow gamecore.cpp.
//   - Positions are kept as float32 across ticks; only quantize (round to int)
//     when comparing against network/ghost snapshot positions.
package physics

import "math"

// Vec2 is a 2D vector of float32, mirroring DDNet's vec2.
type Vec2 struct {
	X, Y float32
}

func add(a, b Vec2) Vec2           { return Vec2{a.X + b.X, a.Y + b.Y} }
func sub(a, b Vec2) Vec2           { return Vec2{a.X - b.X, a.Y - b.Y} }
func scale(a Vec2, s float32) Vec2 { return Vec2{a.X * s, a.Y * s} }

func length(v Vec2) float32 {
	return float32(math.Sqrt(float64(v.X*v.X + v.Y*v.Y)))
}

func distance(a, b Vec2) float32 { return length(sub(a, b)) }

// normalize returns the unit vector, or the zero vector when v is zero,
// matching DDNet's normalize().
func normalize(v Vec2) Vec2 {
	d := length(v)
	if d == 0 {
		return Vec2{}
	}
	l := 1.0 / d
	return Vec2{v.X * l, v.Y * l}
}

func mix(a, b Vec2, t float32) Vec2 {
	return Vec2{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t}
}

// closestPointOnLine returns the point on segment a→b nearest to p (clamped to
// the segment) and ok=false for a degenerate zero-length segment, mirroring
// DDNet's closest_point_on_line (base/vmath.h). Used by the hook→player attach
// test (gamecore.cpp): a peer is grabbed when its body is within PhysicalSize+2
// of this point.
func closestPointOnLine(a, b, p Vec2) (Vec2, bool) {
	ab := sub(b, a)
	sq := dot(ab, ab)
	if sq <= 0 {
		return Vec2{}, false
	}
	t := dot(sub(p, a), ab) / sq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return add(a, scale(ab, t)), true
}

// saturatedAdd adds modifier to current but never pushes it past the
// min/max bound it is heading toward (CCharacterCore SaturatedAdd).
func saturatedAdd(min, max, current, modifier float32) float32 {
	if modifier < 0 {
		if current < min {
			return current
		}
		current += modifier
		if current < min {
			current = min
		}
		return current
	}
	if current > max {
		return current
	}
	current += modifier
	if current > max {
		current = max
	}
	return current
}

// velocityRamp reproduces DDNet's VelocityRamp used in Move(). Below Start
// the ramp is 1.0; above it horizontal velocity is curved down so top speed
// does not grow linearly.
func velocityRamp(value, start, rng, curvature float32) float32 {
	if value < start {
		return 1.0
	}
	return float32(1.0 / math.Pow(float64(curvature), float64((value-start)/rng)))
}

// Input is the subset of CNetObj_PlayerInput the physics tick consumes.
type Input struct {
	Direction int // -1, 0, +1
	TargetX   int // aim target (relative to player), used for hook direction
	TargetY   int
	Jump      bool
	Hook      bool

	// FireGrenade requests a grenade shot toward the target this tick
	// (edge-triggered like the server's fire counter). The core simulates the
	// projectile and applies the explosion impulse to the tee — needed to plan
	// and verify the ghost's rocket-jump sections offline.
	FireGrenade bool

	// JumpTick gives sub-frame jump-edge precision when a single ghost frame
	// spans multiple server ticks (DDNet ghosts sample every 2 ticks). It is
	// the tick offset within the frame's span at which the jump key is first
	// pressed; the jump is then held to the end of the span. 0 presses from the
	// first tick (the default). Core.Tick IGNORES this field — it is a per-tick
	// input. JumpTick is interpreted only by the replay frame->tick expansion.
	JumpTick int
}
