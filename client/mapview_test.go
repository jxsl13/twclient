package client

import (
	"testing"

	"github.com/jxsl13/twmap"
)

func buildTestMap() *twmap.Map {
	// 3x1 game layer: air, solid, freeze.
	game := twmap.Layer{
		Kind: twmap.LayerKindGame, Width: 3, Height: 1,
		Tiles: []twmap.Tile{
			{ID: twmap.TileAir},
			{ID: twmap.TileSolid},
			{ID: twmap.TileFreeze},
		},
	}
	// tune layer: zone 0, 2, 0.
	tune := twmap.Layer{
		Kind: twmap.LayerKindTune, Width: 3, Height: 1,
		TuneTiles: []twmap.TuneTile{{Number: 0}, {Number: 2}, {Number: 0}},
	}
	return &twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game, tune}}}}
}

// V26/V28: MapView spans the whole map, all layers, OOB → solid.
func TestMapViewLayers(t *testing.T) {
	v := NewMapView(buildTestMap())

	if v.Width() != 3 || v.Height() != 1 {
		t.Fatalf("bounds: want 3x1, got %dx%d", v.Width(), v.Height())
	}
	if v.Solid(0, 0) {
		t.Error("(0,0) air should not be solid")
	}
	if !v.Solid(1, 0) {
		t.Error("(1,0) should be solid")
	}
	if !v.Freeze(2, 0) {
		t.Error("(2,0) should be freeze")
	}
	// Out of bounds is the solid world border.
	if !v.Solid(-1, 0) || !v.Solid(3, 0) || !v.Solid(0, 5) {
		t.Error("OOB should be solid")
	}
	// Tune zone from the tune layer.
	if z := v.TuneZone(1, 0); z != 2 {
		t.Errorf("tune zone at (1,0): want 2, got %d", z)
	}
	if z := v.TuneZone(0, 0); z != 0 {
		t.Errorf("tune zone at (0,0): want 0, got %d", z)
	}
}

// V27: Window is fixed-size and pads OOB with ClassSolid.
func TestMapViewWindow(t *testing.T) {
	v := NewMapView(buildTestMap())

	// 3x3 window centered on the solid tile (1,0).
	win := v.Window(1, 0, 1, 1)
	if len(win) != 9 {
		t.Fatalf("window size: want 9, got %d", len(win))
	}
	// Row layout: rows ty=-1,0,1 ; cols tx=0,1,2.
	// Top row (ty=-1) all OOB → solid; bottom row (ty=1) all OOB → solid.
	for i := 0; i < 3; i++ {
		if win[i] != ClassSolid {
			t.Errorf("top row cell %d should be solid (OOB), got %d", i, win[i])
		}
		if win[6+i] != ClassSolid {
			t.Errorf("bottom row cell %d should be solid (OOB), got %d", i, win[6+i])
		}
	}
	// Middle row: air, solid, freeze.
	if win[3] != ClassAir || win[4] != ClassSolid || win[5] != ClassFreeze {
		t.Errorf("middle row classes wrong: %v", win[3:6])
	}
}
