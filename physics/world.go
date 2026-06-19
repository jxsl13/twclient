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
	for i, c := range cores {
		c.Tick(inputs[i])
	}
	for _, c := range cores {
		c.tickDeferredCollision(cores)
	}
	for _, c := range cores {
		c.Move()
		c.quantize()
	}
}

// tickDeferredCollision applies DDNet's tee↔tee push/damp to c's velocity from
// every OTHER core's (last-tick) position (gamecore.cpp:476-498). Collision is
// the vanilla default (m_Tuning.m_PlayerCollision on). Hook-drag is T204; the
// hook-pos follow tail is already handled in Tick/Move for the no-other-core
// case (single-tee hook scenarios are at parity, T198).
func (c *Core) tickDeferredCollision(all []*Core) {
	for _, o := range all {
		if o == c {
			continue
		}
		dist := distance(c.Pos, o.Pos)
		if dist <= 0 {
			continue
		}
		if dist < PhysicalSize*1.25 {
			dir := normalize(sub(c.Pos, o.Pos))
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
	}
}

// dot is the 2D dot product.
func dot(a, b Vec2) float32 { return a.X*b.X + a.Y*b.Y }
