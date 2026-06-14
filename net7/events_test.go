package net7

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

func newTestSession() *Session {
	s := &Session{}
	s.reader.eventCh = make(chan packet.Event, 16)
	return s
}

func recv(t *testing.T, s *Session) packet.Event {
	t.Helper()
	select {
	case ev := <-s.reader.eventCh:
		return ev
	default:
		t.Fatal("expected an event, got none")
		return nil
	}
}

func packInts(v ...int) []byte {
	var b []byte
	for _, x := range v {
		b = append(b, packer.PackInt(x)...)
	}
	return b
}

// V17: 0.7 chat decodes to the same unified events as 0.6.
func TestChatModes07(t *testing.T) {
	s := newTestSession()

	// Public chat: mode ALL.
	s.processChat(append(packInts(chatModeAll, 4, -1), packer.PackString("hi")...))
	if e, ok := recv(t, s).(packet.EventChat); !ok || e.ClientID != 4 || e.Msg != "hi" {
		t.Errorf("0.7 chat wrong: %#v", e)
	}

	// Whisper: mode WHISPER, from 6 to 2.
	s.processChat(append(packInts(chatModeWhisper, 6, 2), packer.PackString("hey")...))
	if e, ok := recv(t, s).(packet.EventWhisper); !ok || e.FromID != 6 || e.ToID != 2 {
		t.Errorf("0.7 whisper wrong: %#v", e)
	}

	// Server message: mode NONE.
	s.processChat(append(packInts(chatModeNone, -1, -1), packer.PackString("srv")...))
	if e, ok := recv(t, s).(packet.EventServerMsg); !ok || e.Msg != "srv" {
		t.Errorf("0.7 server msg wrong: %#v", e)
	}
}

// V15a: 0.7 messages that are 0.6 snapshot objects map to the same events.
func TestObjAsMessage07(t *testing.T) {
	s := newTestSession()

	// ClientInfo -> PlayerJoin. Fields: cid, local, team, name, clan, country,
	// 6 skin parts, 6 useCustomColor, 6 colors, silent.
	var ci []byte
	ci = append(ci, packInts(5, 0, 0)...)
	ci = append(ci, packer.PackString("nameless")...)
	ci = append(ci, packer.PackString("clan")...)
	ci = append(ci, packInts(-1)...) // country
	for i := 0; i < 6; i++ {
		ci = append(ci, packer.PackString("part")...)
	}
	ci = append(ci, packInts(0, 0, 0, 0, 0, 0)...) // useCustomColors
	ci = append(ci, packInts(0, 0, 0, 0, 0, 0)...) // colors
	ci = append(ci, packInts(0)...)                // silent
	s.processClientInfo(ci)
	if e, ok := recv(t, s).(packet.EventPlayerJoin); !ok || e.ClientID != 5 || e.Name != "nameless" {
		t.Errorf("0.7 clientinfo->join wrong: %#v", e)
	}

	// ClientDrop -> PlayerLeave with reason.
	s.processClientDrop(append(packInts(5), append(packer.PackString("timeout"), packInts(0)...)...))
	if e, ok := recv(t, s).(packet.EventPlayerLeave); !ok || e.ClientID != 5 || e.Reason != "timeout" {
		t.Errorf("0.7 clientdrop->leave wrong: %#v", e)
	}
}

func TestVoteAndKill07(t *testing.T) {
	s := newTestSession()

	// VoteSet: clientID, type, timeout, desc, reason.
	vs := append(packInts(-1, 0, 25), append(packer.PackString("kick x"), packer.PackString("afk")...)...)
	s.processVoteSet(vs)
	if e, ok := recv(t, s).(packet.EventVoteSet); !ok || e.Timeout != 25 || e.Desc != "kick x" {
		t.Errorf("0.7 voteset wrong: %#v", e)
	}

	s.processKillMsg(packInts(2, 5, 1, 0))
	if e, ok := recv(t, s).(packet.EventKill); !ok || e.Victim != 5 || e.Weapon != packet.WeaponHammer {
		t.Errorf("0.7 killmsg wrong: %#v", e)
	}
}
