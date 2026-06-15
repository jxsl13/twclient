package net6

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// processSnapMulti: the single-part fast path and the multi-part assembly path
// (business logic for the chunked snapshot stream, V133).
func TestProcessSnapMulti(t *testing.T) {
	s := newTestSessionLive(t)

	// numParts == 1 → processed immediately.
	s.processSnapMulti(packInts(200, -1, 1, 0, 0, 0))
	if _, ok := recv(t, s).(packet.EventSnapshot); !ok {
		t.Error("snap-multi(1 part) did not emit a snapshot")
	}

	// numParts == 2 → assembled across two messages, then processed.
	s.processSnapMulti(packInts(202, -1, 2, 0, 0, 0)) // part 0 → buffered, no event
	select {
	case ev := <-s.reader.eventCh:
		t.Errorf("partial multi-snap emitted early: %#v", ev)
	default:
	}
	s.processSnapMulti(packInts(202, -1, 2, 1, 0, 0)) // part 1 → complete
	if _, ok := recv(t, s).(packet.EventSnapshot); !ok {
		t.Error("snap-multi(2 parts) did not emit a snapshot after assembly")
	}
}

// DDRace EX legacy messages (race-finish + checkpoint + record), via processEx.
func TestProcessDDRaceEx(t *testing.T) {
	s := newTestSessionLive(t)

	// DDRaceTime: time, checkpoint-diff, finish → RaceFinish + Checkpoint events.
	s.processEx(append(uuidSvDDRaceTime[:], packInts(1500, 300, 1)...))
	saw := map[string]bool{}
	for range 2 {
		switch recv(t, s).(type) {
		case packet.EventRaceFinish:
			saw["finish"] = true
		case packet.EventCheckpoint:
			saw["check"] = true
		}
	}
	if !saw["finish"] || !saw["check"] {
		t.Errorf("ddrace-time events missing: %v", saw)
	}

	// Record: server-best + player-best.
	s.processEx(append(uuidSvRecord[:], packInts(9000, 9500)...))
	if e, ok := recv(t, s).(packet.EventRecord); !ok || e.ServerBestCentis != 9000 {
		t.Errorf("record decode wrong: %#v", e)
	}
}

// forceAckSnap, SnapItemSize, SetMap, and the remaining message constructors.
func TestReaderHelpersAndConstructors(t *testing.T) {
	s := newTestSessionLive(t)
	s.forceAckSnap(42) // sends an ack input over the local conn

	if SnapItemSize(ObjCharacter) <= 0 {
		t.Error("SnapItemSize(ObjCharacter) <= 0")
	}
	_ = SnapItemSize(-12345) // unknown type → default branch

	s.SetMap(nil, packet.MapInfo{Name: "dm1"})
	if s.MapName() != "dm1" {
		t.Errorf("SetMap: MapName = %q, want dm1", s.MapName())
	}

	for name, b := range map[string][]byte{
		"CtrlConnect":       CtrlConnect(packet.Token{}),
		"CtrlConnectAccept": CtrlConnectAccept(packet.Token{}),
		"CtrlClose":         CtrlClose("bye"),
		"SysPing":           SysPing(),
		"GameClChangeInfo":  GameClChangeInfo("n", "c", -1, "default", false, 0, 0),
	} {
		if len(b) == 0 {
			t.Errorf("%s produced empty bytes", name)
		}
	}
}
