package net7

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// Covers the 0.7 server-message PARSERS (business logic: wire → unified event),
// complementing events_test.go (V133).

func TestProcessMiscMessages07(t *testing.T) {
	s := newTestSession()

	s.processMotd(packer.PackString("motd07"))
	if e, ok := recv(t, s).(packet.EventMotd); !ok || e.Text != "motd07" {
		t.Errorf("motd: %#v", e)
	}
	s.processRconLine(packer.PackString("> done"))
	if e, ok := recv(t, s).(packet.EventRconLine); !ok || e.Line != "> done" {
		t.Errorf("rcon-line: %#v", e)
	}
	s.processBroadcast(packer.PackString("bc"))
	if e, ok := recv(t, s).(packet.EventBroadcast); !ok || e.Text != "bc" {
		t.Errorf("broadcast: %#v", e)
	}
	s.processTeam(packInts(4, 2, 0))
	if e, ok := recv(t, s).(packet.EventTeamSet); !ok || e.ClientID != 4 || e.Team != 2 {
		t.Errorf("team: %#v", e)
	}
	s.processTuneParams(packInts(1, 2, 3, 4))
	if e, ok := recv(t, s).(packet.EventTuneParams); !ok || len(e.Raw) != 4 {
		t.Errorf("tune-params: %#v", e)
	}
	s.processWeaponPickup(packInts(int(packet.WeaponLaser)))
	if e, ok := recv(t, s).(packet.EventWeaponPickup); !ok || e.Weapon != packet.WeaponLaser {
		t.Errorf("weapon-pickup: %#v", e)
	}
	s.processEmoticon(packInts(3, 1))
	if e, ok := recv(t, s).(packet.EventEmoticon); !ok || e.ClientID != 3 {
		t.Errorf("emoticon: %#v", e)
	}
	s.processSkinChange(append(packInts(7), packer.PackString("santa")...))
	if e, ok := recv(t, s).(packet.EventSkinChange); !ok || e.ClientID != 7 || e.Skin != "santa" {
		t.Errorf("skin-change: %#v", e)
	}
}

func TestProcessVoteAndGameMessages07(t *testing.T) {
	s := newTestSession()

	s.processVoteStatus(packInts(3, 1, 0, 5))
	if e, ok := recv(t, s).(packet.EventVoteStatus); !ok || e.Yes != 3 || e.Total != 5 {
		t.Errorf("vote-status: %#v", e)
	}
	s.processVoteOptionAdd(packer.PackString("map ctf1"))
	if e, ok := recv(t, s).(packet.EventVoteOption); !ok || e.Op != packet.VoteOptionAdd {
		t.Errorf("vote-option add: %#v", e)
	}
	s.processVoteOptionRemove(packer.PackString("map ctf1"))
	if e, ok := recv(t, s).(packet.EventVoteOption); !ok || e.Op != packet.VoteOptionRemove {
		t.Errorf("vote-option remove: %#v", e)
	}
	s.processVoteClearOptions()
	if e, ok := recv(t, s).(packet.EventVoteOption); !ok || e.Op != packet.VoteOptionClear {
		t.Errorf("vote-option clear: %#v", e)
	}

	s.processServerSettings(packInts(1, 4, 0, 1, 0, 12))
	if e, ok := recv(t, s).(packet.EventServerSettings); !ok || !e.KickVote || e.KickMin != 4 || e.PlayerSlots != 12 {
		t.Errorf("server-settings: %#v", e)
	}
	s.processGameInfo(packInts(2, 100, 600))
	if e, ok := recv(t, s).(packet.EventGameInfo); !ok || e.GameFlags != 2 || e.ScoreLimit != 100 || e.TimeLimit != 600 {
		t.Errorf("game-info: %#v", e)
	}
	// GameMsg: first int is the id, the rest are params.
	s.processGameMsg(packInts(5, 9, 9))
	if e, ok := recv(t, s).(packet.EventGameMsg); !ok || e.GameMsgID != 5 || len(e.Params) != 2 {
		t.Errorf("game-msg: %#v", e)
	}
}

func TestProcessCommandInfo07(t *testing.T) {
	s := newTestSession()
	ci := append(append(packer.PackString("kick"), packer.PackString("kick a player")...), packer.PackString("i?r")...)
	s.processCommandInfo(ci)
	if e, ok := recv(t, s).(packet.EventCommandInfo); !ok || e.Name != "kick" || e.Help != "kick a player" {
		t.Errorf("command-info: %#v", e)
	}
	s.processCommandInfoRemove(packer.PackString("kick"))
	if e, ok := recv(t, s).(packet.EventCommandInfoRemove); !ok || e.Name != "kick" {
		t.Errorf("command-info-remove: %#v", e)
	}
}

func TestMaybeParseCapabilities07(t *testing.T) {
	s := newTestSession()
	s.maybeParseCapabilities(append(uuidCapabilities[:], packInts(1, 3)...))
	if got, want := s.caps, packet.ParseServerCapabilities(1, 3); got != want {
		t.Errorf("caps = %#v, want %#v", got, want)
	}
	before := s.caps
	var other [16]byte
	s.maybeParseCapabilities(append(other[:], packInts(1, 7)...))
	if s.caps != before {
		t.Errorf("caps changed on non-capabilities UUID")
	}
	s.maybeParseCapabilities([]byte{0x01})
	if s.caps != before {
		t.Errorf("caps changed on short payload")
	}
}
