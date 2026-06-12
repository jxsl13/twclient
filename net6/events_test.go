package net6

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

func TestProcessChatVariants(t *testing.T) {
	s := newTestSession()

	// Normal public chat: team ALL (-2), author 4.
	s.processChat(append(packInts(-2, 4), packer.PackStr("hello")...))
	if e, ok := recv(t, s).(packet.EventChat); !ok || e.ClientID != 4 || e.Msg != "hello" {
		t.Errorf("chat decode wrong: %#v", e)
	}

	// Server message: author -1.
	s.processChat(append(packInts(-2, -1), packer.PackStr("srv")...))
	if e, ok := recv(t, s).(packet.EventServerMsg); !ok || e.Msg != "srv" {
		t.Errorf("server-msg decode wrong: %#v", e)
	}

	// Whisper received: team WHISPER_RECV (3), from 7.
	s.processChat(append(packInts(TeamWhisperRecv, 7), packer.PackStr("psst")...))
	if e, ok := recv(t, s).(packet.EventWhisper); !ok || e.FromID != 7 || e.Msg != "psst" {
		t.Errorf("whisper decode wrong: %#v", e)
	}
}

func TestProcessKillMsg(t *testing.T) {
	s := newTestSession()
	s.processKillMsg(packInts(2, 5, 3, 0))
	e, ok := recv(t, s).(packet.EventKill)
	if !ok || e.Killer != 2 || e.Victim != 5 || e.Weapon != packet.WeaponShotgun {
		t.Errorf("killmsg decode wrong: %#v", e)
	}
}

func TestProcessVoteSetAndStatus(t *testing.T) {
	s := newTestSession()
	s.processVoteSet(append(append(packInts(30), packer.PackStr("kick foo")...), packer.PackStr("spam")...))
	if e, ok := recv(t, s).(packet.EventVoteSet); !ok || e.Timeout != 30 || e.Desc != "kick foo" || e.Reason != "spam" {
		t.Errorf("voteset decode wrong: %#v", e)
	}

	s.processVoteStatus(packInts(3, 1, 0, 5))
	if e, ok := recv(t, s).(packet.EventVoteStatus); !ok || e.Yes != 3 || e.No != 1 || e.Total != 5 {
		t.Errorf("votestatus decode wrong: %#v", e)
	}
}

func TestProcessEmoticonBroadcast(t *testing.T) {
	s := newTestSession()
	s.processEmoticon(packInts(6, 2))
	if e, ok := recv(t, s).(packet.EventEmoticon); !ok || e.ClientID != 6 || e.Emoticon != packet.EmoticonHearts {
		t.Errorf("emoticon decode wrong: %#v", e)
	}

	s.processBroadcast(packer.PackStr("server restarting"))
	if e, ok := recv(t, s).(packet.EventBroadcast); !ok || e.Text != "server restarting" {
		t.Errorf("broadcast decode wrong: %#v", e)
	}
}
