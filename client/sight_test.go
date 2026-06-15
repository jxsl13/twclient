package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// V143: EventPlayerEnterSight carries the entering tee's position (X, Y) from
// the snapshot char at the entering edge.
func TestEnterSightCarriesPosition(t *testing.T) {
	var ss SnapStorage
	ss.localCID = 1
	s := &packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		charItemFull(2, CharacterState{X: 320, Y: 240}),
	}}
	ss.processSnapshot(s)

	var found *packet.EventPlayerEnterSight
	for _, e := range ss.deriveEvents() {
		if ev, ok := e.(packet.EventPlayerEnterSight); ok && ev.ClientID == 2 {
			ev := ev
			found = &ev
		}
	}
	if found == nil {
		t.Fatal("no EventPlayerEnterSight for tee 2")
	}
	if found.X != 320 || found.Y != 240 {
		t.Errorf("enter-sight pos = (%d,%d), want (320,240)", found.X, found.Y)
	}
}

// V143: the sight/move callback wrappers register and dispatch like any On[E].
func TestSightCallbacks(t *testing.T) {
	c := &Client{}
	var enter, leave, move int
	var enterPos [2]int
	c.OnPlayerEnterSight(func(_ *Client, e packet.EventPlayerEnterSight) {
		enter++
		enterPos = [2]int{e.X, e.Y}
	})
	c.OnPlayerLeaveSight(func(_ *Client, _ packet.EventPlayerLeaveSight) { leave++ })
	c.OnPlayerMove(func(_ *Client, _ packet.EventPlayerMove) { move++ })

	c.callbacks.dispatch(c, packet.EventPlayerEnterSight{ClientID: 2, X: 5, Y: 7})
	c.callbacks.dispatch(c, packet.EventPlayerLeaveSight{ClientID: 2})
	c.callbacks.dispatch(c, packet.EventPlayerMove{ClientID: 2, X: 1, Y: 2})

	if enter != 1 || leave != 1 || move != 1 {
		t.Fatalf("dispatch counts enter=%d leave=%d move=%d, want 1/1/1", enter, leave, move)
	}
	if enterPos != [2]int{5, 7} {
		t.Errorf("enter-sight callback pos = %v, want [5 7]", enterPos)
	}
}
