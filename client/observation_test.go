package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
)

// V27/V28/V30: BuildObservation produces a fixed-size, ego-centric,
// multi-channel observation with static, per-tile tuning, and dynamic planes.
func TestBuildObservation(t *testing.T) {
	c := &Client{predTun: tuningFromRaw(nil)} // default gravity 0.5
	c.snap.localCID = 1
	// 3x1 map: air, solid, freeze.
	game := twmap.Layer{Kind: twmap.LayerKindGame, Width: 3, Height: 1,
		Tiles: []twmap.Tile{{ID: twmap.TileAir}, {ID: twmap.TileSolid}, {ID: twmap.TileFreeze}}}
	c.mapView = NewMapView(&twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game}}}})

	st := TickState{
		LocalID: 1,
		Players: map[int]CharacterState{
			1: {X: 0, Y: 0, Weapon: 2, Health: 7}, // local at tile (0,0)
			2: {X: 64, Y: 0},                      // other at tile (2,0)
		},
		Map:          c.mapView,
		ActiveTuning: c.predTun,
	}

	obs := c.BuildObservation(st, ObsConfig{HalfW: 1, HalfH: 1})

	if obs.W != 3 || obs.H != 3 {
		t.Fatalf("dims: want 3x3, got %dx%d", obs.W, obs.H)
	}
	// Fixed size = W*H per plane.
	if len(obs.Static["solid"]) != 9 || len(obs.Tuning["gravity"]) != 9 {
		t.Fatalf("plane sizes wrong")
	}
	// Center cell (row1,col1)=index 4 holds the self marker.
	if obs.Dynamic["self"][4] != 1 {
		t.Errorf("self should be at window center, plane=%v", obs.Dynamic["self"])
	}
	// Per-tile tuning plane reflects the active gravity (0.5) everywhere.
	for i, g := range obs.Tuning["gravity"] {
		if g != 0.5 {
			t.Errorf("gravity plane cell %d: want 0.5, got %v", i, g)
		}
	}
	// Top and bottom rows are OOB → solid border.
	for col := 0; col < 3; col++ {
		if obs.Static["solid"][col] != 1 || obs.Static["solid"][6+col] != 1 {
			t.Errorf("OOB rows should be solid")
		}
	}
	// Weapon one-hot: shotgun (snap weapon id 2).
	if obs.Scalars["weapon_shotgun"] != 1 || obs.Scalars["weapon_hammer"] != 0 {
		t.Errorf("weapon one-hot wrong: %v", obs.Scalars)
	}
	if obs.Scalars["health"] != 7 {
		t.Errorf("health scalar: want 7, got %v", obs.Scalars["health"])
	}
}

// V27: the observation size is constant regardless of position (incl. near the
// map edge, where the window spills out of bounds).
func TestObservationFixedSizeAtEdge(t *testing.T) {
	c := &Client{predTun: tuningFromRaw(nil)}
	game := twmap.Layer{Kind: twmap.LayerKindGame, Width: 1, Height: 1, Tiles: []twmap.Tile{{}}}
	c.mapView = NewMapView(&twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game}}}})

	st := TickState{LocalID: 0, Players: map[int]CharacterState{0: {X: 0, Y: 0}}, Map: c.mapView}
	obs := c.BuildObservation(st, ObsConfig{HalfW: 4, HalfH: 4})
	if obs.W != 9 || obs.H != 9 || len(obs.Static["solid"]) != 81 {
		t.Errorf("edge window must stay fixed 9x9, got %dx%d", obs.W, obs.H)
	}
	_ = packet.WeaponGun // keep import
}
