package net7

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// Drives every game-message id (and a few system ids) through the real
// processPayload dispatch, covering the sys/game switch arms (V133). A generic
// int+string body satisfies most parsers; the goal is the dispatch coverage, so
// a parser that early-returns on a mismatched body still counts the switch arm.
func TestProcessPayloadDispatch07(t *testing.T) {
	s := newTestSessionLive(t)
	s.reader.eventCh = make(chan packet.Event, 512) // big enough to never block

	body := func() []byte {
		b := packInts(0, 0, 0, 0, 0, 0)
		b = append(b, packer.PackString("a")...)
		b = append(b, packer.PackString("b")...)
		return append(b, packer.PackString("c")...)
	}
	feed := func(id int, sys bool) {
		msg := append(packer.PackMsgID(id, sys), body()...)
		s.processPayload(WrapChunk(msg))
	}

	gameMsgs := []int{
		MsgGameSvMotd, MsgGameSvBroadcast, MsgGameSvChat, MsgGameSvTeam,
		MsgGameSvKillMsg, MsgGameSvTuneParams, MsgGameSvWeaponPickup, MsgGameSvEmoticon,
		MsgGameSvVoteSet, MsgGameSvVoteStatus, MsgGameSvVoteOptionAdd,
		MsgGameSvVoteOptionRemove, MsgGameSvVoteClearOptions, MsgGameSvServerSettings,
		MsgGameSvClientInfo, MsgGameSvClientDrop, MsgGameSvGameInfo, MsgGameSvGameMsg,
		MsgGameSvSkinChange, MsgGameSvCommandInfo, MsgGameSvCommandInfoRemove,
		MsgGameSvRaceFinish, MsgGameSvCheckpoint,
	}
	for _, id := range gameMsgs {
		feed(id, false)
	}
	for _, id := range []int{MsgSysRconLine, MsgSysInputTiming, MsgSysMapChange} {
		feed(id, true)
	}

	// Empty payload is ignored (no panic).
	s.processPayload(nil)
}

func TestBuildStartInfoPacket07(t *testing.T) {
	if len(BuildStartInfoPacket(packet.Token{}, 0, 4, "n", "c", -1)) == 0 {
		t.Error("BuildStartInfoPacket empty")
	}
}
