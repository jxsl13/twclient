package physics

import "testing"

// V70: a nil map must not panic; NewCollision(nil) yields an empty world where
// every tile is the solid out-of-bounds border, and NewCore(nil, …) ticks safely.
func TestNilMapCollisionSafe(t *testing.T) {
	col := NewCollision(nil)
	if !col.Solid(0, 0) {
		t.Error("empty world should report solid everywhere (OOB border)")
	}
	if col.Freeze(5, 5) {
		t.Error("empty world should have no freeze tiles")
	}

	core := NewCore(nil, Vec2{X: 0, Y: 0}) // nil collision
	core.Tick(Input{})                     // must not panic
}
