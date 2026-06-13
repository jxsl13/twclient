package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
	"github.com/jxsl13/twmap"
)

func driverTestClient() (*Client, *stubSession) {
	s := &stubSession{}
	c := &Client{sess: s, predTun: tuningFromRaw(nil)}
	c.snap.localCID = 1
	game := twmap.Layer{Kind: twmap.LayerKindGame, Width: 2, Height: 1, Tiles: []twmap.Tile{{}, {}}}
	c.mapView = NewMapView(&twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game}}}})
	c.snap.processSnapshot(&packet.Snapshot{Tick: 7, Items: []packet.SnapItem{charItem(1, 0, 0)}})
	return c, s
}

// V31: a fixed-cadence dispatch reaches all fixed observers + the controller,
// and only the controller's actions are applied.
func TestDispatchFixedMultiConsumer(t *testing.T) {
	c, s := driverTestClient()
	o1 := &recObserver{mode: TickModeFixed}
	o2 := &recObserver{mode: TickModeFixed}
	frameOnly := &recObserver{mode: TickModeFrame}
	ctrl := &recController{mode: TickModeFixed, emit: []Action{ActKill{}}}
	c.AddObserver(o1)
	c.AddObserver(o2)
	c.AddObserver(frameOnly)
	c.SetController(ctrl)

	c.dispatchFixed(c.buildTickState())

	if o1.ticks != 1 || o2.ticks != 1 {
		t.Errorf("both fixed observers should see the tick: %d %d", o1.ticks, o2.ticks)
	}
	if frameOnly.ticks != 0 {
		t.Errorf("frame observer must not receive fixed dispatch: %d", frameOnly.ticks)
	}
	if len(ctrl.seen) != 1 {
		t.Errorf("controller should see the tick: %d", len(ctrl.seen))
	}
	if s.lastCall != "kill" {
		t.Errorf("controller action not applied: %q", s.lastCall)
	}
}

// V20: observers never act, even if they are the only consumer.
func TestObserversDoNotAct(t *testing.T) {
	c, s := driverTestClient()
	c.AddObserver(&recObserver{mode: TickModeFixed})
	c.dispatchFixed(c.buildTickState())
	if s.lastCall != "" {
		t.Errorf("observer-only must apply no actions, got %q", s.lastCall)
	}
}

// V24: both cadences share the one builder; frame overlays IntraTick + smoothing.
func TestFrameStateOverlaysSmoothing(t *testing.T) {
	c, _ := driverTestClient()
	col := openCollision()
	tun := tuningFromRaw(nil)
	c.predictEnabled = true
	c.prevPredWorld = newPredictedWorld(col, tun, physics.DefaultWorldConfig(), 0, map[int]CharacterState{1: {X: 0, Y: 0}})
	c.predWorld = newPredictedWorld(col, tun, physics.DefaultWorldConfig(), 0, map[int]CharacterState{1: {X: 100, Y: 0}})

	fixed := c.buildTickState()
	if fixed.IntraTick != 0 {
		t.Errorf("fixed IntraTick must be 0, got %v", fixed.IntraTick)
	}
	frame := c.buildFrameState(0.5)
	if frame.IntraTick != 0.5 || frame.Tick != fixed.Tick {
		t.Errorf("frame state wrong: intra=%v tick=%d", frame.IntraTick, frame.Tick)
	}
	if frame.Players[1].X != 50 {
		t.Errorf("frame smoothed X: want 50, got %d", frame.Players[1].X)
	}
}
