package client

import "github.com/jxsl13/twclient/physics"

// ObsConfig configures the ego-centric observation window (V27). The window is
// (2*HalfW+1) x (2*HalfH+1) tiles, centered on the local player; a square
// (HalfW==HalfH) is the recommended default.
type ObsConfig struct {
	HalfW int
	HalfH int
}

// Observation is a fixed-size, multi-channel, ego-centric view for ML (V27/V28/
// V30). Each plane is a row-major W*H grid. Planes are grouped (static map,
// per-tile tuning, dynamic entities); Scalars hold per-tick agent values. The
// dimensions are constant for a given ObsConfig regardless of map or position.
type Observation struct {
	W, H    int
	Static  map[string][]float32 // collision/gameplay layers, 0/1 (or zone index)
	Tuning  map[string][]float32 // per-tile tuning param value (V30)
	Dynamic map[string][]float32 // entities rasterized into the window
	Scalars map[string]float32   // agent scalars (weapon one-hot, hp, vel, …)
}

// staticChannels lists the boolean map planes in a stable order.
var staticChannels = []string{
	"solid", "unhook", "hookthrough", "death", "freeze", "tele", "speedup", "switch", "tunezone",
}

// tuningChannels lists the per-tile tuning planes (the movement-relevant subset).
var tuningChannels = []string{
	"gravity", "ground_control_speed", "ground_control_accel", "ground_friction",
	"air_control_speed", "air_friction", "hook_length", "hook_drag_speed",
}

func tuningValue(t physics.Tuning, name string) float32 {
	switch name {
	case "gravity":
		return t.Gravity
	case "ground_control_speed":
		return t.GroundControlSpeed
	case "ground_control_accel":
		return t.GroundControlAccel
	case "ground_friction":
		return t.GroundFriction
	case "air_control_speed":
		return t.AirControlSpeed
	case "air_friction":
		return t.AirFriction
	case "hook_length":
		return t.HookLength
	case "hook_drag_speed":
		return t.HookDragSpeed
	default:
		return 0
	}
}

// BuildObservation rasterizes a fixed-size ego-centric observation from the
// tick state (V27/V28/V30). The window is centered on the local player's tile;
// out-of-bounds cells read the solid border. Static + per-tile tuning planes
// come from the map; dynamic planes from the predicted entities.
func (c *Client) BuildObservation(st TickState, cfg ObsConfig) Observation {
	w := 2*cfg.HalfW + 1
	h := 2*cfg.HalfH + 1
	n := w * h

	obs := Observation{
		W: w, H: h,
		Static:  make(map[string][]float32, len(staticChannels)),
		Tuning:  make(map[string][]float32, len(tuningChannels)),
		Dynamic: make(map[string][]float32, 3),
		Scalars: make(map[string]float32),
	}
	for _, ch := range staticChannels {
		obs.Static[ch] = make([]float32, n)
	}
	for _, ch := range tuningChannels {
		obs.Tuning[ch] = make([]float32, n)
	}
	obs.Dynamic["self"] = make([]float32, n)
	obs.Dynamic["players"] = make([]float32, n)
	obs.Dynamic["projectiles"] = make([]float32, n)

	// Center on the local player's tile.
	self, hasSelf := st.Players[st.LocalID]
	cx, cy := 0, 0
	if hasSelf {
		cx, cy = self.X/physics.TileSize, self.Y/physics.TileSize
	}

	mv := st.Map
	idx := func(col, row int) int { return row*w + col }

	// Static + tuning planes.
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			tx := cx - cfg.HalfW + col
			ty := cy - cfg.HalfH + row
			i := idx(col, row)
			if mv != nil {
				if mv.Solid(tx, ty) {
					obs.Static["solid"][i] = 1
				}
				if mv.Unhook(tx, ty) {
					obs.Static["unhook"][i] = 1
				}
				if mv.HookThrough(tx, ty) {
					obs.Static["hookthrough"][i] = 1
				}
				if mv.Death(tx, ty) {
					obs.Static["death"][i] = 1
				}
				if mv.Freeze(tx, ty) {
					obs.Static["freeze"][i] = 1
				}
				if mv.Tele(tx, ty) {
					obs.Static["tele"][i] = 1
				}
				if mv.Speedup(tx, ty) {
					obs.Static["speedup"][i] = 1
				}
				if mv.Switch(tx, ty) {
					obs.Static["switch"][i] = 1
				}
				obs.Static["tunezone"][i] = float32(mv.TuneZone(tx, ty))
			} else {
				obs.Static["solid"][i] = 1 // no map → solid border
			}
			tun := c.TuningAt(tx, ty)
			for _, name := range tuningChannels {
				obs.Tuning[name][i] = tuningValue(tun, name)
			}
		}
	}

	// Dynamic planes: rasterize entities into window cells.
	mark := func(plane []float32, worldX, worldY int) {
		col := worldX/physics.TileSize - (cx - cfg.HalfW)
		row := worldY/physics.TileSize - (cy - cfg.HalfH)
		if col >= 0 && col < w && row >= 0 && row < h {
			plane[idx(col, row)] = 1
		}
	}
	for cid, ch := range st.Players {
		if cid == st.LocalID {
			mark(obs.Dynamic["self"], ch.X, ch.Y)
		} else {
			mark(obs.Dynamic["players"], ch.X, ch.Y)
		}
	}
	for _, p := range st.Projectiles {
		mark(obs.Dynamic["projectiles"], p.X, p.Y)
	}

	// Scalars.
	if hasSelf {
		for wpn := 0; wpn <= 6; wpn++ {
			v := float32(0)
			if int(self.Weapon) == wpn {
				v = 1
			}
			obs.Scalars["weapon_"+weaponName(wpn)] = v
		}
		obs.Scalars["health"] = float32(self.Health)
		obs.Scalars["armor"] = float32(self.Armor)
		obs.Scalars["ammo"] = float32(self.AmmoCount)
		obs.Scalars["vel_x"] = float32(self.VelX)
		obs.Scalars["vel_y"] = float32(self.VelY)
		obs.Scalars["hook_state"] = float32(self.HookState)
	}
	obs.Scalars["self_tune_zone"] = float32(st.SelfTuneZone)
	active := st.ActiveTuning
	obs.Scalars["active_gravity"] = active.Gravity
	obs.Scalars["game_state_flags"] = float32(st.GameInfo.GameStateFlags)

	return obs
}

func weaponName(w int) string {
	switch w {
	case 0:
		return "hammer"
	case 1:
		return "gun"
	case 2:
		return "shotgun"
	case 3:
		return "grenade"
	case 4:
		return "laser"
	case 5:
		return "ninja"
	default:
		return "x"
	}
}
