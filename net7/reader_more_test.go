package net7

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// 0.7 race/checkpoint/input-timing parsers + the ack/SetMap helpers (V133).
func TestProcessRaceAndTiming07(t *testing.T) {
	s := newTestSessionLive(t)

	s.processRaceFinish(packInts(1500, 0, 4))
	if e, ok := recv(t, s).(packet.EventRaceFinish); !ok || e.TimeCentis != 1500 || !e.Finish {
		t.Errorf("race-finish: %#v", e)
	}
	s.processCheckpoint(packInts(300))
	if e, ok := recv(t, s).(packet.EventCheckpoint); !ok || e.DiffCentis != 300 {
		t.Errorf("checkpoint: %#v", e)
	}
	s.processInputTiming(packInts(120, 7))
	if e, ok := recv(t, s).(packet.EventInputTiming); !ok || e.IntendedTick != 120 || e.TimeLeft != 7 {
		t.Errorf("input-timing: %#v", e)
	}

	s.forceAckSnap(42)

	s.SetMap(nil, packet.MapInfo{Name: "ctf1"})
	if s.MapName() != "ctf1" {
		t.Errorf("SetMap: MapName = %q, want ctf1", s.MapName())
	}
}
