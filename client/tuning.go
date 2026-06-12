package client

import "github.com/jxsl13/twclient/physics"

// tuningFromRaw decodes a Sv_TuneParams network array (DDNet tuning.h order,
// each value fixed-point ×100) into a physics.Tuning. Fields absent from
// physics.Tuning are ignored; missing trailing values keep their defaults.
func tuningFromRaw(raw []int32) physics.Tuning {
	t := physics.DefaultTuning()
	get := func(i int) (float32, bool) {
		if i < 0 || i >= len(raw) {
			return 0, false
		}
		return float32(raw[i]) / 100, true
	}
	set := func(dst *float32, i int) {
		if v, ok := get(i); ok {
			*dst = v
		}
	}
	// Index → field, per src/game/tuning.h MACRO_TUNING_PARAM order.
	set(&t.GroundControlSpeed, 0)
	set(&t.GroundControlAccel, 1)
	set(&t.GroundFriction, 2)
	set(&t.GroundJumpImpulse, 3)
	set(&t.AirJumpImpulse, 4)
	set(&t.AirControlSpeed, 5)
	set(&t.AirControlAccel, 6)
	set(&t.AirFriction, 7)
	set(&t.HookLength, 8)
	set(&t.HookFireSpeed, 9)
	set(&t.HookDragAccel, 10)
	set(&t.HookDragSpeed, 11)
	set(&t.Gravity, 12)
	set(&t.VelrampStart, 13)
	set(&t.VelrampRange, 14)
	set(&t.VelrampCurvature, 15)
	set(&t.GunCurvature, 16)
	set(&t.GunSpeed, 17)
	set(&t.ShotgunCurvature, 19)
	set(&t.ShotgunSpeed, 20)
	set(&t.GrenadeCurvature, 23)
	set(&t.GrenadeSpeed, 24)
	set(&t.GrenadeLifetime, 25)
	set(&t.ExplosionStrength, 35)
	return t
}

// setTuning records the default (zone-0) server tuning, decoded from a
// Sv_TuneParams message. Per-zone values are not delivered over the wire (V29);
// unknown zones fall back to this default.
func (c *Client) setTuning(raw []int32) {
	tun := tuningFromRaw(raw)
	c.mu.Lock()
	c.predTun = tun
	if c.tunings == nil {
		c.tunings = make(map[int]physics.Tuning)
	}
	c.tunings[0] = tun
	c.mu.Unlock()
}

// tuningForZone returns the tuning for a tune-zone, falling back to the default.
// Caller must hold c.mu.
func (c *Client) tuningForZone(zone int) physics.Tuning {
	if t, ok := c.tunings[zone]; ok {
		return t
	}
	return c.predTun
}

// TuningAt returns the tuning active at a tile, resolved through the map's
// tune-zone layer (V29).
func (c *Client) TuningAt(tx, ty int) physics.Tuning {
	zone := 0
	if mv := c.MapView(); mv != nil {
		zone = mv.TuneZone(tx, ty)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tuningForZone(zone)
}

// ActiveTuning returns the tuning at the local player's predicted tile.
func (c *Client) ActiveTuning() physics.Tuning {
	ch := c.PredictedCharacter()
	return c.TuningAt(ch.X/physics.TileSize, ch.Y/physics.TileSize)
}
