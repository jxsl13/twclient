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

func TestProcessExMessages(t *testing.T) {
	s := newTestSession()

	// KillMsgTeam via the full EX path (UUID + body).
	body := packInts(3, 7)
	msg := append(append([]byte{}, uuidSvKillMsgTeam[:]...), body...)
	s.processEx(msg)
	if e, ok := recv(t, s).(packet.EventKillMsgTeam); !ok || e.Team != 3 || e.First != 7 {
		t.Errorf("ext killmsgteam decode wrong: %#v", e)
	}

	// CommandInfo: name, args, help (note wire order).
	ci := append(uuidSvCommandInfo[:], append(append(packer.PackStr("me"), packer.PackStr("")...), packer.PackStr("show name")...)...)
	s.processEx(ci)
	if e, ok := recv(t, s).(packet.EventCommandInfo); !ok || e.Name != "me" || e.Help != "show name" {
		t.Errorf("ext commandinfo decode wrong: %#v", e)
	}

	// TeamsState: raw per-client team values.
	ts := append(uuidSvTeamsState[:], packInts(0, 5, 0, 5)...)
	s.processEx(ts)
	e, ok := recv(t, s).(packet.EventTeamsState)
	if !ok || e.Team[1] != 5 || e.Team[3] != 5 {
		t.Errorf("ext teamsstate decode wrong: %#v", e)
	}
	if _, present := e.Team[0]; present {
		t.Errorf("teamsstate should omit team-0 players: %#v", e)
	}

	// Unknown UUID is ignored (no event, no panic).
	var unknown [16]byte
	s.processEx(append(unknown[:], packInts(1)...))
	select {
	case ev := <-s.reader.eventCh:
		t.Errorf("unknown ext UUID produced an event: %#v", ev)
	default:
	}
}

func TestProcessSysEvents(t *testing.T) {
	s := newTestSession()

	s.processRconLine(packer.PackStr("[server]: hello"))
	if e, ok := recv(t, s).(packet.EventRconLine); !ok || e.Line != "[server]: hello" {
		t.Errorf("rcon line decode wrong: %#v", e)
	}

	s.processRconAuthStatus(packInts(1))
	if e, ok := recv(t, s).(packet.EventRconAuth); !ok || !e.Authed {
		t.Errorf("rcon auth decode wrong: %#v", e)
	}

	s.processRconCmdAdd(append(append(packer.PackStr("kick"), packer.PackStr("kick a player")...), packer.PackStr("i?r")...))
	if e, ok := recv(t, s).(packet.EventRconCmd); !ok || e.Op != packet.RconCmdAdd || e.Cmd != "kick" {
		t.Errorf("rcon cmd add decode wrong: %#v", e)
	}

	s.processServerError(packer.PackStr("wrong password"))
	if e, ok := recv(t, s).(packet.EventServerError); !ok || e.Msg != "wrong password" {
		t.Errorf("server error decode wrong: %#v", e)
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
