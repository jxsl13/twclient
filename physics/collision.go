package physics

// Collision is the map collision model used by the physics core. It is
// driven by two tile predicates so the physics package stays independent of
// any concrete map representation (the replay package supplies adapters over
// twmap; see replay.NewCollision).
//
// Coordinates passed to the predicates are integer TILE coordinates
// (world / TileSize). Out-of-bounds tiles must be reported as Solid by the
// predicate, matching the bordered-map convention DDNet relies on.
type Collision struct {
	// Solid reports whether the tile blocks movement
	// (TILE_SOLID or TILE_NOHOOK in DDNet terms).
	Solid func(tx, ty int) bool
	// NoHook reports whether the tile is non-hookable (TILE_NOHOOK).
	// May be nil, in which case no tile is treated as non-hookable.
	NoHook func(tx, ty int) bool
	// HookThrough reports whether the HOOK passes through this tile even if
	// it is solid (DDNet front-layer TILE_THROUGH_CUT / TILE_THROUGH_ALL).
	// Movement collision is unaffected. May be nil.
	HookThrough func(tx, ty int) bool
	// Freeze reports whether the tile freezes the tee (TILE_FREEZE /
	// TILE_DEEP_FREEZE). DDRace-only; nil on vanilla maps. The core consults
	// it only when WorldConfig.PredictFreeze is set.
	Freeze func(tx, ty int) bool
}

// roundToInt mirrors DDNet's round_to_int (round half away from zero).
func roundToInt(f float32) int {
	if f > 0 {
		return int(f + 0.5)
	}
	return int(f - 0.5)
}

// tileOf converts a world coordinate to a tile coordinate. Go integer
// division truncates toward zero; negative results land off-map where the
// Solid predicate reports solid, so the truncation difference is harmless on
// bordered maps.
func tileOf(world int) int { return world / TileSize }

// CheckPoint reports whether the given world point lies in a solid tile.
// Equivalent to DDNet CCollision::CheckPoint(x, y).
func (c *Collision) CheckPoint(x, y float32) bool {
	return c.Solid(tileOf(roundToInt(x)), tileOf(roundToInt(y)))
}

// checkNoHook reports whether the world point lies in a non-hookable tile.
func (c *Collision) checkNoHook(x, y float32) bool {
	if c.NoHook == nil {
		return false
	}
	return c.NoHook(tileOf(roundToInt(x)), tileOf(roundToInt(y)))
}

// Frozen reports whether the world point lies in a freeze tile. False when no
// freeze layer is present (vanilla maps).
func (c *Collision) Frozen(x, y float32) bool {
	if c.Freeze == nil {
		return false
	}
	return c.Freeze(tileOf(roundToInt(x)), tileOf(roundToInt(y)))
}

// TestBox reports whether an axis-aligned box of the given size centred at
// pos overlaps any solid tile (CCollision::TestBox).
func (c *Collision) TestBox(pos Vec2, w, h float32) bool {
	w *= 0.5
	h *= 0.5
	return c.CheckPoint(pos.X-w, pos.Y-h) ||
		c.CheckPoint(pos.X+w, pos.Y-h) ||
		c.CheckPoint(pos.X-w, pos.Y+h) ||
		c.CheckPoint(pos.X+w, pos.Y+h)
}

// MoveBox advances pos along vel one tick, resolving collisions against solid
// tiles and zeroing the velocity component on any axis that is blocked
// (CCollision::MoveBox with zero elasticity, as used by CCharacterCore::Move).
//
// It returns whether the box came to rest on the ground this move (a blocked
// downward step), which the core uses to refresh the air jump.
func (c *Collision) MoveBox(pos, vel *Vec2, w, h float32) (grounded bool) {
	dist := length(*vel)
	if dist <= 0.00001 {
		return false
	}

	max := int(dist)
	frac := 1.0 / float32(max+1)
	p := *pos
	v := *vel

	for i := 0; i <= max; i++ {
		np := add(p, scale(v, frac))
		if c.TestBox(np, w, h) {
			hits := 0
			if c.TestBox(Vec2{X: p.X, Y: np.Y}, w, h) {
				if v.Y > 0 {
					grounded = true
				}
				np.Y = p.Y
				v.Y = 0 // elasticity 0
				hits++
			}
			if c.TestBox(Vec2{X: np.X, Y: p.Y}, w, h) {
				np.X = p.X
				v.X = 0
				hits++
			}
			if hits == 0 {
				// Diagonal corner: block both axes.
				if v.Y > 0 {
					grounded = true
				}
				np = p
				v = Vec2{}
			}
		}
		p = np
	}

	*pos = p
	*vel = v
	return grounded
}

// IntersectLine walks the segment p0->p1 and returns the first solid point it
// hits (CCollision::IntersectLine). hit is false if the segment is clear.
// noHook reports whether the hit tile is non-hookable (TILE_NOHOOK), which
// the hook logic uses to decide retract vs grab.
func (c *Collision) IntersectLine(p0, p1 Vec2) (hit bool, at Vec2, noHook bool) {
	d := distance(p0, p1)
	end := int(d) + 1
	for i := 0; i <= end; i++ {
		a := float32(i) / float32(end)
		p := mix(p0, p1, a)
		if c.CheckPoint(p.X, p.Y) {
			return true, p, c.checkNoHook(p.X, p.Y)
		}
	}
	return false, p1, false
}

// hookThroughAt reports whether the hook passes through the tile at the world
// point (front-layer through tiles).
func (c *Collision) hookThroughAt(x, y float32) bool {
	if c.HookThrough == nil {
		return false
	}
	return c.HookThrough(tileOf(roundToInt(x)), tileOf(roundToInt(y)))
}

// IntersectLineHook is IntersectLine with DDNet's hook-through semantics
// (CCollision::IntersectLineTeleHook + IsThrough): solid tiles marked
// hook-through do not stop the hook — it continues and can grab a hookable
// tile behind them. Movement collision is unaffected.
func (c *Collision) IntersectLineHook(p0, p1 Vec2) (hit bool, at Vec2, noHook bool) {
	d := distance(p0, p1)
	end := int(d) + 1
	for i := 0; i <= end; i++ {
		a := float32(i) / float32(end)
		p := mix(p0, p1, a)
		if c.CheckPoint(p.X, p.Y) && !c.hookThroughAt(p.X, p.Y) {
			return true, p, c.checkNoHook(p.X, p.Y)
		}
	}
	return false, p1, false
}
