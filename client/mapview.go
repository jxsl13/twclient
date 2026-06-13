package client

import "github.com/jxsl13/twmap"

// TileClass is the primary collision/gameplay class of a map tile, used for the
// observation window crop. Per-property booleans (Solid, Freeze, …) give the
// full multi-label detail.
type TileClass uint8

// TileClass values — the primary class assigned to each tile for the
// observation crop, mirroring DDNet's collision/entity tile types
// (src/game/collision.cpp, src/game/mapitems.h): empty air, solid wall, unhook,
// hook-through, death, freeze, teleporter, speedup, switch.
const (
	ClassAir TileClass = iota
	ClassSolid
	ClassUnhook
	ClassHookThrough
	ClassDeath
	ClassFreeze
	ClassTele
	ClassSpeedup
	ClassSwitch
)

// MapView is a queryable, read-only view over the COMPLETE local map (V26):
// all DDNet special-tile layers, not just collision. Out-of-bounds queries
// return the solid world border.
type MapView struct {
	w, h    int
	game    []twmap.Tile
	front   []twmap.Tile
	tele    []twmap.TeleTile
	speedup []twmap.SpeedupTile
	switchT []twmap.SwitchTile
	tune    []twmap.TuneTile
}

// NewMapView builds a MapView from a parsed map. The game layer defines the
// tile bounds; the front/tele/speedup/switch/tune layers (when present) share
// those bounds.
func NewMapView(m *twmap.Map) *MapView {
	v := &MapView{}
	if m == nil {
		return v // nil map → empty 0×0 view; all queries return ClassSolid (V70)
	}
	for _, grp := range m.Groups {
		for _, l := range grp.Layers {
			switch l.Kind {
			case twmap.LayerKindGame:
				if v.game == nil {
					v.w, v.h, v.game = l.Width, l.Height, l.Tiles
				}
			case twmap.LayerKindFront:
				v.front = l.Tiles
			case twmap.LayerKindTele:
				v.tele = l.TeleTiles
			case twmap.LayerKindSpeedup:
				v.speedup = l.SpeedupTiles
			case twmap.LayerKindSwitch:
				v.switchT = l.SwitchTiles
			case twmap.LayerKindTune:
				v.tune = l.TuneTiles
			}
		}
	}
	return v
}

// Width is the map width in tiles.
func (v *MapView) Width() int { return v.w }

// Height is the map height in tiles.
func (v *MapView) Height() int { return v.h }

func (v *MapView) inBounds(tx, ty int) bool {
	return tx >= 0 && ty >= 0 && tx < v.w && ty < v.h
}

func (v *MapView) gameID(tx, ty int) uint8 {
	if !v.inBounds(tx, ty) {
		return twmap.TileSolid
	}
	idx := ty*v.w + tx
	if idx >= len(v.game) {
		return twmap.TileSolid
	}
	return v.game[idx].ID
}

// Solid reports whether the tile blocks movement. OOB is solid (world border).
func (v *MapView) Solid(tx, ty int) bool { return twmap.IsSolid(v.gameID(tx, ty)) }

// Unhook reports whether the hook cannot attach to the tile.
func (v *MapView) Unhook(tx, ty int) bool { return v.gameID(tx, ty) == twmap.TileUnhookable }

// Death reports whether the tile kills on contact.
func (v *MapView) Death(tx, ty int) bool { return v.gameID(tx, ty) == twmap.TileDeath }

// Finish reports whether the tile is a race finish tile.
func (v *MapView) Finish(tx, ty int) bool { return v.gameID(tx, ty) == twmap.TileFinish }

// Start reports whether the tile is a race start tile.
func (v *MapView) Start(tx, ty int) bool { return v.gameID(tx, ty) == twmap.TileStart }

// Freeze reports whether the tile freezes the tee (freeze or deep-freeze).
func (v *MapView) Freeze(tx, ty int) bool {
	id := v.gameID(tx, ty)
	return id == twmap.TileFreeze || id == twmap.TileDeepFreeze
}

// HookThrough reports whether the hook passes through this tile (front layer).
func (v *MapView) HookThrough(tx, ty int) bool {
	if v.front == nil || !v.inBounds(tx, ty) {
		return false
	}
	idx := ty*v.w + tx
	if idx >= len(v.front) {
		return false
	}
	id := v.front[idx].ID
	return id == twmap.TileThroughCut || id == twmap.TileThrough
}

// Tele reports whether a teleporter tile is present (tele layer).
func (v *MapView) Tele(tx, ty int) bool {
	if v.tele == nil || !v.inBounds(tx, ty) {
		return false
	}
	idx := ty*v.w + tx
	return idx < len(v.tele) && v.tele[idx].ID != 0
}

// Speedup reports whether a speed-boost tile is present (speedup layer).
func (v *MapView) Speedup(tx, ty int) bool {
	if v.speedup == nil || !v.inBounds(tx, ty) {
		return false
	}
	idx := ty*v.w + tx
	return idx < len(v.speedup) && v.speedup[idx].Force != 0
}

// Switch reports whether a switch tile is present (switch layer).
func (v *MapView) Switch(tx, ty int) bool {
	if v.switchT == nil || !v.inBounds(tx, ty) {
		return false
	}
	idx := ty*v.w + tx
	return idx < len(v.switchT) && v.switchT[idx].Number != 0
}

// TuneZone returns the tune-zone index at the tile (0 = default / no tune
// layer). Drives position-dependent tuning (V29).
func (v *MapView) TuneZone(tx, ty int) int {
	if v.tune == nil || !v.inBounds(tx, ty) {
		return 0
	}
	idx := ty*v.w + tx
	if idx >= len(v.tune) {
		return 0
	}
	return int(v.tune[idx].Number)
}

// IsDDRace reports whether the map carries DDRace-only features: any of the
// tele/speedup/switch/tune special layers, or a freeze tile in the game layer.
// Vanilla maps have none of these. Drives the predicted-world physics config
// (V10b).
func (v *MapView) IsDDRace() bool {
	if v.tele != nil || v.speedup != nil || v.switchT != nil || v.tune != nil {
		return true
	}
	for _, t := range v.game {
		if t.ID == twmap.TileFreeze || t.ID == twmap.TileDeepFreeze {
			return true
		}
	}
	return false
}

// Tile returns the primary class of a tile (most movement-relevant first), for
// the observation crop. OOB → ClassSolid.
func (v *MapView) Tile(tx, ty int) TileClass {
	switch {
	case !v.inBounds(tx, ty):
		return ClassSolid
	case v.Unhook(tx, ty):
		return ClassUnhook
	case v.Solid(tx, ty):
		return ClassSolid
	case v.Death(tx, ty):
		return ClassDeath
	case v.Freeze(tx, ty):
		return ClassFreeze
	case v.HookThrough(tx, ty):
		return ClassHookThrough
	case v.Tele(tx, ty):
		return ClassTele
	case v.Speedup(tx, ty):
		return ClassSpeedup
	case v.Switch(tx, ty):
		return ClassSwitch
	default:
		return ClassAir
	}
}

// Window returns a fixed-size (2*halfW+1) x (2*halfH+1) crop of tile classes
// centered on (cx,cy), row-major. Out-of-bounds cells are ClassSolid (V26/V27).
func (v *MapView) Window(cx, cy, halfW, halfH int) []TileClass {
	w := 2*halfW + 1
	h := 2*halfH + 1
	out := make([]TileClass, 0, w*h)
	for ty := cy - halfH; ty <= cy+halfH; ty++ {
		for tx := cx - halfW; tx <= cx+halfW; tx++ {
			out = append(out, v.Tile(tx, ty))
		}
	}
	return out
}
