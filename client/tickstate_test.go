package client

import (
	"testing"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
)

// V19: buildTickState assembles a complete, self-contained observation.
func TestBuildTickStateComplete(t *testing.T) {
	c := &Client{predTun: tuningFromRaw(nil)}
	c.snap.localCID = 1

	// Minimal map so MapView is available.
	game := twmap.Layer{Kind: twmap.LayerKindGame, Width: 2, Height: 1, Tiles: []twmap.Tile{{}, {}}}
	c.mapView = NewMapView(&twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game}}}})

	// Snapshot with the local char, a pickup, a flag, a laser.
	c.snap.processSnapshot(&packet.Snapshot{Tick: 42, Items: []packet.SnapItem{
		charItem(1, 0, 0),
		{TypeID: net6.ObjPickup, ID: 1, Fields: []int{10, 20, 1, 0}},
		{TypeID: net6.ObjFlag, ID: 2, Fields: []int{30, 40, 0}},
		{TypeID: net6.ObjLaser, ID: 3, Fields: []int{1, 2, 3, 4, 5}},
	}})

	// An event since last tick.
	c.tickEvents = []packet.Event{packet.EventChat{ClientID: 1, Msg: "hi"}}

	st := c.buildTickState()

	if st.Tick != 42 || st.LocalID != 1 {
		t.Errorf("tick/local wrong: %d %d", st.Tick, st.LocalID)
	}
	if _, ok := st.Players[1]; !ok {
		t.Error("local player missing from Players")
	}
	if len(st.Pickups) != 1 || st.Pickups[0].X != 10 {
		t.Errorf("pickups wrong: %#v", st.Pickups)
	}
	if len(st.Flags) != 1 || st.Flags[0].Team != 0 {
		t.Errorf("flags wrong: %#v", st.Flags)
	}
	if len(st.Lasers) != 1 || st.Lasers[0].StartTick != 5 {
		t.Errorf("lasers wrong: %#v", st.Lasers)
	}
	if st.Map == nil {
		t.Error("map view missing")
	}
	if st.IntraTick != 0 {
		t.Errorf("canonical build IntraTick must be 0, got %v", st.IntraTick)
	}
	if len(st.Events) != 1 {
		t.Errorf("events since tick: want 1, got %d", len(st.Events))
	}

	// Events drained — a second build sees none.
	if st2 := c.buildTickState(); len(st2.Events) != 0 {
		t.Errorf("events should drain: got %d", len(st2.Events))
	}
}
