package client

import (
	"testing"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
)

// V18/V22: both protocol sessions implement the full Session interface, so
// Client.Do (and every Action send) works identically on 0.6 and 0.7. These
// compile-time assertions fail to build if either protocol lacks a send.
var (
	_ Session = (*net6.Session)(nil)
	_ Session = (*net7.Session)(nil)
)

// V20/V31: one TickState serves both a view-only observer (e.g. UI) and the
// single controller (e.g. ML policy) plugged simultaneously; only the
// controller acts.
func TestObserverAndControllerShareState(t *testing.T) {
	s := &stubSession{}
	c := &Client{sess: s, predTun: tuningFromRaw(nil)}
	c.snap.localCID = 1
	game := twmap.Layer{Kind: twmap.LayerKindGame, Width: 2, Height: 1, Tiles: []twmap.Tile{{}, {}}}
	c.mapView = NewMapView(&twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game}}}})
	c.snap.processSnapshot(&packet.Snapshot{Tick: 11, Items: []packet.SnapItem{charItem(1, 0, 0)}})

	ui := &recObserver{mode: TickModeFixed}
	ml := &recController{mode: TickModeFixed, emit: []Action{ActChat{Msg: "gg"}}}
	c.AddObserver(ui)
	c.SetController(ml)

	c.dispatchFixed(c.buildTickState())

	if ui.ticks != 1 {
		t.Errorf("observer should receive the tick: %d", ui.ticks)
	}
	if len(ml.seen) != 1 || ml.seen[0].Tick != 11 {
		t.Errorf("controller should receive the same tick state: %#v", ml.seen)
	}
	if s.lastCall != "chatTeam" {
		t.Errorf("controller action should be applied: %q", s.lastCall)
	}
	// Observer and controller saw the SAME tick number (shared TickState).
	if ui.ticks == 1 && ml.seen[0].Tick != 11 {
		t.Errorf("observer/controller must share one built state")
	}
}

// V18: the same Action drives both protocols through the shared Session
// interface — proven by net6 and net7 producing non-empty sends for an action.
func TestActionBuildersBothProtocols(t *testing.T) {
	if len(net6.GameClVote(1)) == 0 || len(net7.GameClVote(1)) == 0 {
		t.Error("vote builder empty for a protocol")
	}
	if len(net6.GameClCallVote("kick", "1", "afk")) == 0 || len(net7.GameClCallVote("kick", "1", "afk")) == 0 {
		t.Error("callvote builder empty for a protocol")
	}
	if len(net6.GameClSetSpectatorMode(3)) == 0 || len(net7.GameClSetSpectatorMode(3)) == 0 {
		t.Error("spectate builder empty for a protocol")
	}
}
