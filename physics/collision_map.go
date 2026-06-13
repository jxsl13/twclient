package physics

import "github.com/jxsl13/twmap"

// Front-layer tile IDs that let hooks pass through solid tiles (DDNet
// mapitems.h: TILE_THROUGH_CUT=5, TILE_THROUGH_ALL=6).
const (
	tileThroughCut uint8 = 5
	tileThroughAll uint8 = 6
)

// NewCollision builds a Collision model from a parsed map's game layer plus
// the front layer's hook-through tiles. Out-of-bounds and missing tiles are
// treated as solid, matching DDNet's world border behaviour.
func NewCollision(m *twmap.Map) *Collision {
	var tiles []twmap.Tile
	var w, h int
	if layers := m.GameLayers(); len(layers) > 0 {
		gl := layers[0]
		tiles, w, h = gl.Tiles, gl.Width, gl.Height
	}

	at := func(tx, ty int) uint8 {
		if tx < 0 || ty < 0 || tx >= w || ty >= h {
			return twmap.TileSolid
		}
		idx := ty*w + tx
		if idx >= len(tiles) {
			return twmap.TileSolid
		}
		return tiles[idx].ID
	}

	col := &Collision{
		Solid:  func(tx, ty int) bool { return twmap.IsSolid(at(tx, ty)) },
		NoHook: func(tx, ty int) bool { return at(tx, ty) == twmap.TileUnhookable },
		Freeze: func(tx, ty int) bool {
			id := at(tx, ty)
			return id == twmap.TileFreeze || id == twmap.TileDeepFreeze
		},
	}

	// Front layer: hook-through tiles let the hook pass through solid tiles.
	for _, grp := range m.Groups {
		for _, l := range grp.Layers {
			if l.Kind != twmap.LayerKindFront || l.Width == 0 {
				continue
			}
			front := l
			col.HookThrough = func(tx, ty int) bool {
				if tx < 0 || ty < 0 || tx >= front.Width || ty >= front.Height {
					return false
				}
				id := front.Tiles[ty*front.Width+tx].ID
				return id == tileThroughCut || id == tileThroughAll
			}
			return col
		}
	}
	return col
}
