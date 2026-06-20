package physics

// WorldStep advances a SET of cores by one tick in lockstep, matching DDNet's
// CWorldCore tick (prediction/gameworld.cpp:223/232 — all Tick(), then all the
// deferred N² tee↔tee interaction, then Move/Quantize). Positions change only
// in Move(), so the deferred pass reads stable last-tick positions and writes
// each core's OWN velocity → order-independent for collision (T199, V149/V21).
//
// inputs is aligned with cores (inputs[i] drives cores[i]). A single-element
// set has no other cores, so the deferred pass is a no-op and WorldStep reduces
// to Core.Step — single-tee parity (T198) is preserved.
func WorldStep(cores []*Core, inputs []Input) {
	// Expose the world set to each core so the hook→player attach loop in Tick
	// (T203) and the hook-drag in the deferred pass (T204) can see the peers.
	for i, c := range cores {
		c.id = i
		c.peers = cores
	}
	// Advance Tick + the deferred pair-interaction together, core by core, in
	// index order. This matches DDNet's sequential per-core CCharacterCore::Tick
	// (the driver runs all Tick() before any Move()): a hook-drag applied to a
	// peer lands on that peer's velocity BEFORE the peer's own Tick adds gravity
	// this turn, which an all-Tick-then-all-interaction split would get wrong
	// (the SaturatedAdd ±HookDragSpeed clamp is order-sensitive once vy>15). The
	// interaction reads only last-tick positions (Move runs after), so the push
	// half stays order-independent (T199 parity preserved).
	for i, c := range cores {
		c.Tick(inputs[i])
		c.tickDeferredCollision(cores)
	}
	for _, c := range cores {
		c.Move()
		c.quantize()
	}
}

// tickDeferredCollision applies DDNet's per-pair tee↔tee influence from c to
// every OTHER core, reading their (last-tick) positions and writing velocity
// (gamecore.cpp:476-513). Two effects, both from the vanilla defaults:
//   - collision push/damp when bodies overlap (m_PlayerCollision on);
//   - hook drag when c has hooked o (m_PlayerHooking on) — pulls the hooked
//     peer toward c by 1.5× and nudges c toward the peer by 0.25×, each capped
//     to ±HookDragSpeed via SaturatedAdd.
//
// Collision writes only c's velocity, so it is order-independent. Hook drag
// writes BOTH c's and the peer's velocity; the cores are visited in index
// order here, matching DDNet's sequential per-core Tick (T204).
func (c *Core) tickDeferredCollision(all []*Core) {
	for i, o := range all {
		if o == c {
			continue
		}
		dist := distance(c.Pos, o.Pos)
		if dist <= 0 {
			continue
		}
		dir := normalize(sub(c.Pos, o.Pos))

		// tee↔tee collision push/damp.
		if dist < PhysicalSize*1.25 {
			a := PhysicalSize*1.45 - dist
			velocity := float32(0.5)
			// Don't add excess force: weight by direction vs current velocity
			// (gamecore.cpp:491-493). Note DDNet keeps this in float deliberately.
			if length(c.Vel) > 0.0001 {
				velocity = 1 - (dot(normalize(c.Vel), dir)+1)/2
			}
			c.Vel = add(c.Vel, scale(dir, a*(velocity*0.75)))
			c.Vel = scale(c.Vel, 0.85)
		}

		// Hook drag: c has hooked o → pull both (gamecore.cpp:502-513). dir
		// points from o toward c, so the hooked peer gets +dir (toward the
		// hooker) and the hooker gets -dir (toward the peer).
		if c.HookedPlayer == i && dist > PhysicalSize*1.5 {
			hookAccel := c.tun.HookDragAccel * (dist / c.tun.HookLength)
			drag := c.tun.HookDragSpeed
			o.Vel = Vec2{
				X: saturatedAdd(-drag, drag, o.Vel.X, hookAccel*dir.X*1.5),
				Y: saturatedAdd(-drag, drag, o.Vel.Y, hookAccel*dir.Y*1.5),
			}
			c.Vel = Vec2{
				X: saturatedAdd(-drag, drag, c.Vel.X, -hookAccel*dir.X*0.25),
				Y: saturatedAdd(-drag, drag, c.Vel.Y, -hookAccel*dir.Y*0.25),
			}
		}
	}
}

// dot is the 2D dot product.
func dot(a, b Vec2) float32 { return a.X*b.X + a.Y*b.Y }
