package client

import "testing"

// V70: NewMapView(nil) must not panic; it yields an empty 0×0 view whose every
// query returns the solid border.
func TestNilMapViewSafe(t *testing.T) {
	v := NewMapView(nil)
	if v == nil {
		t.Fatal("NewMapView(nil) should return an empty view, not nil")
	}
	if v.Width() != 0 || v.Height() != 0 {
		t.Errorf("empty view dims = %dx%d, want 0x0", v.Width(), v.Height())
	}
	if v.Tile(3, 4) != ClassSolid {
		t.Errorf("empty view should report ClassSolid everywhere, got %v", v.Tile(3, 4))
	}
}
