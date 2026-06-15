package net6

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// Covers the remaining 0.6 server-message PARSERS (business logic: wire → event),
// complementing events_test.go. Each feeds a synthetic packed body to the
// process* handler and asserts the decoded event (V133).

func TestProcessMiscGameMessages(t *testing.T) {
	s := newTestSession()

	s.processMotd(packer.PackString("welcome"))
	if e, ok := recv(t, s).(packet.EventMotd); !ok || e.Text != "welcome" {
		t.Errorf("motd decode wrong: %#v", e)
	}

	s.processSoundGlobal(packInts(7))
	if e, ok := recv(t, s).(packet.EventSoundGlobal); !ok || e.SoundID != 7 {
		t.Errorf("sound-global decode wrong: %#v", e)
	}

	s.processWeaponPickup(packInts(int(packet.WeaponGrenade)))
	if e, ok := recv(t, s).(packet.EventWeaponPickup); !ok || e.Weapon != packet.WeaponGrenade {
		t.Errorf("weapon-pickup decode wrong: %#v", e)
	}

	// TuneParams reads ints until the buffer is exhausted.
	s.processTuneParams(packInts(10, 20, 30))
	if e, ok := recv(t, s).(packet.EventTuneParams); !ok || len(e.Raw) != 3 || e.Raw[2] != 30 {
		t.Errorf("tune-params decode wrong: %#v", e)
	}
}

func TestProcessVoteOptions(t *testing.T) {
	s := newTestSession()

	s.processVoteOptionAdd(packer.PackString("map dm1"))
	if e, ok := recv(t, s).(packet.EventVoteOption); !ok || e.Op != packet.VoteOptionAdd || e.Desc != "map dm1" {
		t.Errorf("vote-option add wrong: %#v", e)
	}

	s.processVoteOptionRemove(packer.PackString("map dm1"))
	if e, ok := recv(t, s).(packet.EventVoteOption); !ok || e.Op != packet.VoteOptionRemove {
		t.Errorf("vote-option remove wrong: %#v", e)
	}

	s.processVoteClearOptions()
	if e, ok := recv(t, s).(packet.EventVoteOption); !ok || e.Op != packet.VoteOptionClear {
		t.Errorf("vote-option clear wrong: %#v", e)
	}

	// List-add emits one event per description.
	body := append(packInts(2), packer.PackString("a")...)
	body = append(body, packer.PackString("b")...)
	s.processVoteOptionListAdd(body)
	if e, ok := recv(t, s).(packet.EventVoteOption); !ok || e.Desc != "a" {
		t.Errorf("vote-option list[0] wrong: %#v", e)
	}
	if e, ok := recv(t, s).(packet.EventVoteOption); !ok || e.Desc != "b" {
		t.Errorf("vote-option list[1] wrong: %#v", e)
	}
}

func TestProcessRconCmdRemove(t *testing.T) {
	s := newTestSession()
	s.processRconCmdRem(packer.PackString("kick"))
	if e, ok := recv(t, s).(packet.EventRconCmd); !ok || e.Op != packet.RconCmdRemove || e.Cmd != "kick" {
		t.Errorf("rcon cmd remove wrong: %#v", e)
	}
}

// EX (DDNet UUID-keyed) parsers, driven through the processEx dispatch.
func TestProcessExMoreMessages(t *testing.T) {
	s := newTestSession()

	s.processEx(append(uuidSvYourVote[:], packInts(1)...))
	if e, ok := recv(t, s).(packet.EventYourVote); !ok || e.Voted != 1 {
		t.Errorf("ext yourvote wrong: %#v", e)
	}

	// RaceFinish: clientID then time (centiseconds).
	s.processEx(append(uuidSvRaceFinish[:], packInts(4, 1234)...))
	if e, ok := recv(t, s).(packet.EventRaceFinish); !ok || e.TimeCentis != 1234 || !e.Finish {
		t.Errorf("ext racefinish wrong: %#v", e)
	}

	s.processEx(append(uuidSvCommandInfoRemove[:], packer.PackString("kick")...))
	if e, ok := recv(t, s).(packet.EventCommandInfoRemove); !ok || e.Name != "kick" {
		t.Errorf("ext commandinfo-remove wrong: %#v", e)
	}

	s.processEx(append(uuidSvChangeInfoCooldown[:], packInts(900)...))
	if e, ok := recv(t, s).(packet.EventChangeInfoCooldown); !ok || e.WaitUntilTick != 900 {
		t.Errorf("ext change-info-cooldown wrong: %#v", e)
	}

	s.processEx(append(uuidSvMapSoundGlobal[:], packInts(2)...))
	if e, ok := recv(t, s).(packet.EventMapSoundGlobal); !ok || e.SoundID != 2 {
		t.Errorf("ext map-sound-global wrong: %#v", e)
	}
}

func TestMaybeParseCapabilities(t *testing.T) {
	s := newTestSession()

	// Valid capabilities EX: UUID + version + flags → stored.
	s.maybeParseCapabilities(append(uuidCapabilities[:], packInts(1, 3)...))
	if got, want := s.caps, packet.ParseServerCapabilities(1, 3); got != want {
		t.Errorf("caps = %#v, want %#v", got, want)
	}

	// Wrong UUID → ignored (caps unchanged).
	before := s.caps
	var other [16]byte
	s.maybeParseCapabilities(append(other[:], packInts(1, 7)...))
	if s.caps != before {
		t.Errorf("caps changed on non-capabilities UUID: %#v", s.caps)
	}

	// Too-short payload (< 16 bytes) → ignored, no panic.
	s.maybeParseCapabilities([]byte{0x01, 0x02})
	if s.caps != before {
		t.Errorf("caps changed on short payload: %#v", s.caps)
	}
}
