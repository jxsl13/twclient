package physics

import (
	"testing"

	"github.com/jxsl13/twmap"
)

// V8: NewCollision migrated out of replay; builds a usable Collision from a map.
func TestNewCollisionEmptyMap(t *testing.T) {
	col := NewCollision(&twmap.Map{})
	if col == nil || col.Solid == nil || col.NoHook == nil {
		t.Fatal("NewCollision returned nil collision or nil predicate")
	}
	// Empty map: no game layer, every tile is out-of-bounds -> solid border.
	if !col.Solid(0, 0) {
		t.Error("empty map: (0,0) should be solid border")
	}
	if !col.Solid(-5, -5) {
		t.Error("out-of-bounds should be solid")
	}
	if col.NoHook(0, 0) {
		t.Error("empty map: border is solid, not unhookable")
	}
}

// V8: a constructed game layer is queried correctly (air vs solid vs unhookable).
func TestNewCollisionGameLayer(t *testing.T) {
	// 2x1 game layer: (0,0)=air, (1,0)=solid.
	gl := twmap.Layer{
		Kind:   twmap.LayerKindGame,
		Width:  2,
		Height: 1,
		Tiles: []twmap.Tile{
			{ID: twmap.TileAir},
			{ID: twmap.TileSolid},
		},
	}
	m := &twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{gl}}}}

	col := NewCollision(m)
	if col.Solid(0, 0) {
		t.Error("(0,0) air should not be solid")
	}
	if !col.Solid(1, 0) {
		t.Error("(1,0) solid tile should be solid")
	}
}
