package client

import (
	"testing"

	"github.com/jxsl13/twmap"
)

// V29/V9b: Sv_TuneParams raw array decodes into physics.Tuning at the right
// indices (DDNet tuning.h order, fixed-point ×100).
func TestTuningFromRaw(t *testing.T) {
	raw := make([]int32, 36)
	raw[12] = 75   // gravity 0.75
	raw[0] = 1500  // ground control speed 15.0
	raw[17] = 3300 // gun speed 33.0
	raw[24] = 1234 // grenade speed 12.34

	tun := tuningFromRaw(raw)
	if tun.Gravity != 0.75 {
		t.Errorf("gravity: want 0.75, got %v", tun.Gravity)
	}
	if tun.GroundControlSpeed != 15.0 {
		t.Errorf("ground control speed: want 15, got %v", tun.GroundControlSpeed)
	}
	if tun.GunSpeed != 33.0 {
		t.Errorf("gun speed: want 33, got %v", tun.GunSpeed)
	}
	if tun.GrenadeSpeed != 12.34 {
		t.Errorf("grenade speed: want 12.34, got %v", tun.GrenadeSpeed)
	}
}

// V29: TuningAt resolves through the map tune-zone layer; setTuning records the
// default (zone 0). Unknown zones fall back to default.
func TestTuningAtZones(t *testing.T) {
	c := &Client{predTun: tuningFromRaw(nil)}
	// Build a map with a tune layer: tile (1,0) is zone 2.
	game := twmap.Layer{Kind: twmap.LayerKindGame, Width: 3, Height: 1,
		Tiles: []twmap.Tile{{}, {}, {}}}
	tune := twmap.Layer{Kind: twmap.LayerKindTune, Width: 3, Height: 1,
		TuneTiles: []twmap.TuneTile{{Number: 0}, {Number: 2}, {Number: 0}}}
	c.mapView = NewMapView(&twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game, tune}}}})

	// Default tuning: gravity 0.5 from DefaultTuning.
	gravRaw := make([]int32, 36)
	gravRaw[12] = 50 // gravity 0.5
	c.setTuning(gravRaw)

	// No zone-2 tuning recorded → falls back to default (zone 0).
	if g := c.TuningAt(1, 0).Gravity; g != 0.5 {
		t.Errorf("zone-2 fallback gravity: want 0.5, got %v", g)
	}

	// Record a distinct zone-2 tuning (low gravity); TuningAt(1,0) must use it,
	// while the default zone-0 tile (0,0) keeps the default.
	lowGrav := c.predTun
	lowGrav.Gravity = 0.1
	c.tunings[2] = lowGrav
	if g := c.TuningAt(1, 0).Gravity; g != 0.1 {
		t.Errorf("zone-2 gravity: want 0.1, got %v", g)
	}
	if g := c.TuningAt(0, 0).Gravity; g != 0.5 {
		t.Errorf("zone-0 gravity: want 0.5, got %v", g)
	}
}
