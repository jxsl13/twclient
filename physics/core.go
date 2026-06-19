package physics

// grenade is an in-flight projectile fired by the tee (rocket-jump modeling).
// Flight follows DDNet's CalcPos: pos(t) = start + dir*speed*t + curvature
// pulling the y component down quadratically.
type grenade struct {
	start Vec2
	dir   Vec2
	age   int // ticks since fired
}

// Core is a single tee's physics state, a faithful port of DDNet's
// CCharacterCore. Drive it one server tick at a time with Step (or Tick then
// Move). It reads collision through the injected Collision model.
//
// The jump model is edge-triggered exactly like the server:
//   - bit 0 of jumped tracks "a jump is in progress, release required",
//   - bit 1 of jumped tracks "the air jump has been used".
//
// Holding Jump does not retrigger; the input must return to false before the
// next jump edge. The air jump refreshes on landing.
type Core struct {
	Pos Vec2
	Vel Vec2

	Direction int // last applied movement direction (-1,0,+1)
	Angle     int // aim angle * 256, for parity with snapshots

	// Jump state.
	Jumped      int // bitmask: bit0 = in-progress, bit1 = air jump used
	Jumps       int // total jumps available (default 2)
	JumpedTotal int

	// Hook state.
	HookState int
	HookPos   Vec2
	HookDir   Vec2
	HookTick  int

	// In-flight grenades (rocket-jump modeling) and the previous fire input
	// for edge detection.
	grenades []grenade
	prevFire bool

	col *Collision
	tun Tuning
	cfg WorldConfig
}

// NewCore creates a core at pos with default tuning, jumps, and the vanilla
// world config (weapons predicted, no DDRace tile physics).
func NewCore(col *Collision, pos Vec2) *Core {
	if col == nil {
		col = NewCollision(nil) // nil collision → empty all-solid world (V70)
	}
	return &Core{
		Pos:       pos,
		Jumps:     DefaultJumps,
		HookState: HookIdle,
		col:       col,
		tun:       DefaultTuning(),
		cfg:       DefaultWorldConfig(),
	}
}

// SetTuning overrides the physics tuning (e.g. for non-vanilla servers).
func (c *Core) SetTuning(t Tuning) { c.tun = t }

// SetWorldConfig selects which physics subsystems are simulated (vanilla vs
// DDRace, V10b).
func (c *Core) SetWorldConfig(cfg WorldConfig) { c.cfg = cfg }

// Frozen reports whether the tee is currently standing on a freeze tile, which
// only matters when the world config predicts freeze (DDRace).
func (c *Core) Frozen() bool {
	return c.cfg.PredictFreeze && c.col.Frozen(c.Pos.X, c.Pos.Y)
}

// Grounded reports whether the tee is standing on solid ground this tick,
// using the same two foot probes as the server.
func (c *Core) Grounded() bool {
	half := PhysicalSize / 2
	return c.col.CheckPoint(c.Pos.X+half, c.Pos.Y+half+5) ||
		c.col.CheckPoint(c.Pos.X-half, c.Pos.Y+half+5)
}

// Tick advances the core's velocity and hook for one server tick given the
// input, without moving the position (CCharacterCore::Tick). Call Move
// afterwards, or use Step to do both.
func (c *Core) Tick(in Input) {
	grounded := c.Grounded()

	// Freeze tiles (DDRace) suppress all tee control: no movement, jump, hook,
	// or fire while frozen. Gravity and existing velocity still apply, so the
	// tee slides/falls. Gated by WorldConfig.PredictFreeze, so vanilla servers
	// never predict freeze (V10b). The hook is released on entering freeze.
	if c.Frozen() {
		in.Direction = 0
		in.Jump = false
		in.Hook = false
		in.FireGrenade = false
	}

	// Landing refreshes the air jump. The server resets this in Move() from
	// MoveBox's grounded result; resetting from the start-of-tick ground
	// probe yields the same gameplay outcome (air jump available after a
	// landing) and keeps the tick self-contained.
	if grounded {
		c.Jumped &^= 2
		c.JumpedTotal = 0
	}

	targetDir := normalize(Vec2{X: float32(in.TargetX), Y: float32(in.TargetY)})

	c.Vel.Y += c.tun.Gravity

	maxSpeed := c.tun.AirControlSpeed
	accel := c.tun.AirControlAccel
	friction := c.tun.AirFriction
	if grounded {
		maxSpeed = c.tun.GroundControlSpeed
		accel = c.tun.GroundControlAccel
		friction = c.tun.GroundFriction
	}

	c.Direction = in.Direction

	// Edge-triggered jump.
	if in.Jump {
		if c.Jumped&1 == 0 {
			switch {
			case grounded && (c.Jumped&2 == 0 || c.Jumps != 0):
				c.Vel.Y = -c.tun.GroundJumpImpulse
				if c.Jumps > 1 {
					c.Jumped |= 1
				} else {
					c.Jumped |= 3
				}
				c.JumpedTotal = 0
			case c.Jumped&2 == 0:
				c.Vel.Y = -c.tun.AirJumpImpulse
				c.Jumped |= 3
				c.JumpedTotal++
			}
		}
	} else {
		c.Jumped &^= 1
	}

	// Hook launch / release edge.
	if in.Hook {
		if c.HookState == HookIdle {
			c.HookState = HookFlying
			c.HookPos = add(c.Pos, scale(targetDir, PhysicalSize*1.5))
			c.HookDir = targetDir
			c.HookTick = 0
		}
	} else {
		c.HookState = HookIdle
		c.HookPos = c.Pos
	}

	// Horizontal control.
	switch {
	case c.Direction < 0:
		c.Vel.X = saturatedAdd(-maxSpeed, maxSpeed, c.Vel.X, -accel)
	case c.Direction > 0:
		c.Vel.X = saturatedAdd(-maxSpeed, maxSpeed, c.Vel.X, accel)
	default:
		c.Vel.X *= friction
	}

	c.tickHook()

	// Grenades: fire on input edge, then advance in-flight projectiles and
	// apply explosion impulses (rocket jumps).
	if c.cfg.PredictWeapons && in.FireGrenade && !c.prevFire {
		d := targetDir
		if d.X == 0 && d.Y == 0 {
			d = Vec2{X: 1}
		}
		c.grenades = append(c.grenades, grenade{
			start: add(c.Pos, scale(d, PhysicalSize*0.75)),
			dir:   d,
		})
	}
	c.prevFire = in.FireGrenade
	c.tickGrenades()

	// Clamp to a sane maximum (CCharacterCore::Tick).
	if l := length(c.Vel); l > 6000 {
		c.Vel = scale(normalize(c.Vel), 6000)
	}
}

// grenadePos returns the projectile position at age ticks after firing,
// following DDNet's CalcPos (curvature pulls y down quadratically).
func (g *grenade) posAt(tun *Tuning, age int) Vec2 {
	t := float32(age) / TickSpeed * tun.GrenadeSpeed
	return Vec2{
		X: g.start.X + g.dir.X*t,
		Y: g.start.Y + g.dir.Y*t + tun.GrenadeCurvature/10000*t*t,
	}
}

// tickGrenades advances each projectile one tick; on wall hit (or lifetime
// end) it explodes, applying DDNet's radial impulse to the tee:
// force = dir(tee-expl) * strength * falloff * 2, falloff 1 inside 48 units
// shrinking to 0 at 135 (CGameContext::CreateExplosion).
func (c *Core) tickGrenades() {
	const radius, inner = 135.0, 48.0
	keep := c.grenades[:0]
	for i := range c.grenades {
		g := &c.grenades[i]
		p0 := g.posAt(&c.tun, g.age)
		g.age++
		p1 := g.posAt(&c.tun, g.age)
		hit, at, _ := c.col.IntersectLineHook(p0, p1)
		expired := float32(g.age) >= c.tun.GrenadeLifetime*TickSpeed
		if !hit && !expired {
			keep = append(keep, *g)
			continue
		}
		ep := p1
		if hit {
			ep = at
		}
		diff := sub(c.Pos, ep)
		l := length(diff)
		if l > radius {
			continue
		}
		forceDir := Vec2{X: 0, Y: 1}
		if l > 0 {
			forceDir = normalize(diff)
		}
		fall := 1 - clamp01((l-inner)/(radius-inner))
		c.Vel = add(c.Vel, scale(forceDir, c.tun.ExplosionStrength*fall*2))
	}
	c.grenades = keep
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// tickHook advances the hook state machine and applies hook drag, mirroring
// the "do hook" section of CCharacterCore::Tick (single-player paths only;
// player-to-player hooking is not modelled).
func (c *Core) tickHook() {
	switch {
	case c.HookState == HookIdle:
		c.HookPos = c.Pos

	case c.HookState >= HookRetractStart && c.HookState < HookRetractEnd:
		c.HookState++

	case c.HookState == HookRetractEnd:
		c.HookState = HookRetracted

	case c.HookState == HookFlying:
		newPos := add(c.HookPos, scale(c.HookDir, c.tun.HookFireSpeed))
		if distance(c.Pos, newPos) > c.tun.HookLength {
			c.HookState = HookRetractStart
			newPos = add(c.Pos, scale(normalize(sub(newPos, c.Pos)), c.tun.HookLength))
		}
		// Hook-through tiles (front layer) let the hook pass; see
		// Collision.IntersectLineHook.
		if hit, at, noHook := c.col.IntersectLineHook(c.HookPos, newPos); hit {
			newPos = at
			if noHook {
				c.HookState = HookRetractStart
			} else {
				c.HookState = HookGrabbed
			}
		}
		c.HookPos = newPos
	}

	if c.HookState == HookGrabbed {
		if distance(c.HookPos, c.Pos) > 46.0 {
			hookVel := scale(normalize(sub(c.HookPos, c.Pos)), c.tun.HookDragAccel)
			// The hook ignores gravity but should feel similar: dampen
			// downward pull, and favour the direction the tee is steering.
			if hookVel.Y > 0 {
				hookVel.Y *= 0.3
			}
			if (hookVel.X < 0 && c.Direction < 0) || (hookVel.X > 0 && c.Direction > 0) {
				hookVel.X *= 0.95
			} else {
				hookVel.X *= 0.75
			}
			newVel := add(c.Vel, hookVel)
			// Only apply while under the drag speed limit, or if it does not
			// increase overall speed.
			if length(newVel) < c.tun.HookDragSpeed || length(newVel) < length(c.Vel) {
				c.Vel = newVel
			}
		}
		// NOTE: DDNet's 1.2s hook timeout (m_HookTick > TickSpeed+TickSpeed/5)
		// applies ONLY to hooked PLAYERS — wall hooks are endless. We don't
		// model player hooks, so no timeout here (a wrongly applied timeout
		// released the f3000 Tutorial wall hook 10 ticks early).
		c.HookTick++
	}
}

// Move integrates velocity into position for one tick, applying the velocity
// ramp and resolving collisions (CCharacterCore::Move).
func (c *Core) Move() {
	ramp := velocityRamp(length(c.Vel)*TickSpeed, c.tun.VelrampStart, c.tun.VelrampRange, c.tun.VelrampCurvature)
	c.Vel.X *= ramp

	newPos := c.Pos
	grounded := c.col.MoveBox(&newPos, &c.Vel, PhysicalSize, PhysicalSize)

	c.Vel.X *= 1.0 / ramp

	if grounded {
		c.Jumped &^= 2
		c.JumpedTotal = 0
	}

	c.Pos = newPos
}

// Step runs a full server tick: Tick, Move, then Quantize.
func (c *Core) Step(in Input) {
	c.Tick(in)
	c.Move()
	c.quantize()
}

// quantize snaps Pos to integer world coords and Vel to the 1/256 grid, matching
// DDNet's per-tick CCharacterCore::Quantize (Write→Read, gamecore.cpp:694-699).
// DDNet's PREDICTION runs on quantized state — prediction character.cpp:642 calls
// Quantize() every tick, so the rounded pos/vel feed the NEXT tick. Without this
// the float Pos drifts ~4px/scenario from DDNet despite exact velocity (T197/T198,
// V149 quantized-output parity).
func (c *Core) quantize() {
	c.Pos.X = float32(roundToInt(c.Pos.X))
	c.Pos.Y = float32(roundToInt(c.Pos.Y))
	c.Vel.X = float32(roundToInt(c.Vel.X*256)) / 256
	c.Vel.Y = float32(roundToInt(c.Vel.Y*256)) / 256
}

// QuantizedPos returns the position rounded to integer world coordinates,
// matching what a network/ghost snapshot would record.
func (c *Core) QuantizedPos() (x, y int) {
	return roundToInt(c.Pos.X), roundToInt(c.Pos.Y)
}
