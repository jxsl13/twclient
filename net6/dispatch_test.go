package net6

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// feedMsg frames a single 0.6 message (msg id + body) into a non-vital chunk and
// runs it through the real processPayload dispatch — covering the unpack + the
// system/game message switch, the way the live reader does (V133).
func feedMsg(s *Session, msgID int, sys bool, body []byte) {
	msg := append(packer.PackMsgID(msgID, sys), body...)
	s.processPayload(WrapChunk(msg))
}

func TestProcessPayloadDispatch(t *testing.T) {
	s := newTestSession()

	// Game message → SvMotd.
	feedMsg(s, MsgGameSvMotd, false, packer.PackString("rules"))
	if e, ok := recv(t, s).(packet.EventMotd); !ok || e.Text != "rules" {
		t.Errorf("dispatch motd wrong: %#v", e)
	}

	// Game message → SvSoundGlobal.
	feedMsg(s, MsgGameSvSoundGlobal, false, packInts(9))
	if e, ok := recv(t, s).(packet.EventSoundGlobal); !ok || e.SoundID != 9 {
		t.Errorf("dispatch sound-global wrong: %#v", e)
	}

	// System message → RconLine.
	feedMsg(s, MsgSysRconLine, true, packer.PackString("> ok"))
	if e, ok := recv(t, s).(packet.EventRconLine); !ok || e.Line != "> ok" {
		t.Errorf("dispatch rcon-line wrong: %#v", e)
	}

	// System message → Error.
	feedMsg(s, MsgSysError, true, packer.PackString("nope"))
	if e, ok := recv(t, s).(packet.EventServerError); !ok || e.Msg != "nope" {
		t.Errorf("dispatch server-error wrong: %#v", e)
	}

	// Empty chunk payload is ignored (no panic, no event).
	s.processPayload(nil)
	select {
	case ev := <-s.reader.eventCh:
		t.Errorf("empty payload produced an event: %#v", ev)
	default:
	}
}

func TestProcessInputTiming(t *testing.T) {
	s := newTestSession()
	s.processInputTiming(packInts(120, 7))
	if e, ok := recv(t, s).(packet.EventInputTiming); !ok || e.IntendedTick != 120 || e.TimeLeft != 7 {
		t.Errorf("input-timing decode wrong: %#v", e)
	}
	// Truncated (one int) → no event, no panic.
	s.processInputTiming(packInts(1)[:0])
	select {
	case ev := <-s.reader.eventCh:
		t.Errorf("empty input-timing produced an event: %#v", ev)
	default:
	}
}

func TestSkinInts(t *testing.T) {
	full := make([]int, SizeClientInfo)
	if _, ok := SkinInts(full); !ok {
		t.Error("SkinInts(full) ok = false, want true")
	}
	if _, ok := SkinInts([]int{1, 2}); ok {
		t.Error("SkinInts(short) ok = true, want false")
	}
}
