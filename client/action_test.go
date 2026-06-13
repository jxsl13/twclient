package client

import (
	"context"
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
)

// stubSession records the action-relevant sends and no-ops everything else.
type stubSession struct {
	lastCall string
	chatTeam bool
	whisperT int
	emoticon packet.Emoticon
	team     int
	spectate int
	vote     bool
	callVote [3]string
	chatMsg  string
}

func (s *stubSession) Login(context.Context, string, string, ...packet.LoginOption) error {
	return nil
}
func (s *stubSession) Capabilities() packet.ServerCapabilities         { return packet.ServerCapabilities{} }
func (s *stubSession) Close() error                                    { return nil }
func (s *stubSession) StartReader(context.Context)                     {}
func (s *stubSession) EventCh() <-chan packet.Event                    { return nil }
func (s *stubSession) DownloadMap(context.Context) (*twmap.Map, error) { return nil, nil }
func (s *stubSession) Map() *twmap.Map                                 { return nil }
func (s *stubSession) MapName() string                                 { return "" }
func (s *stubSession) GetMapInfo() packet.MapInfo                      { return packet.MapInfo{} }
func (s *stubSession) SetMap(*twmap.Map, packet.MapInfo)               {}
func (s *stubSession) Poll() (packet.Event, error)                     { return nil, nil }
func (s *stubSession) SendInput(int, int, int, []byte) error           { s.lastCall = "input"; return nil }
func (s *stubSession) SendChat(msg string) error {
	s.lastCall, s.chatMsg = "chat", msg
	return nil
}
func (s *stubSession) SendChatTeam(team bool, _ string) error {
	s.lastCall, s.chatTeam = "chatTeam", team
	return nil
}
func (s *stubSession) SendWhisper(toID int, _ string) error {
	s.lastCall, s.whisperT = "whisper", toID
	return nil
}
func (s *stubSession) SendKill() error { s.lastCall = "kill"; return nil }
func (s *stubSession) SendEmoticon(e packet.Emoticon) error {
	s.lastCall, s.emoticon = "emoticon", e
	return nil
}
func (s *stubSession) SendSetTeam(t int) error   { s.lastCall, s.team = "setTeam", t; return nil }
func (s *stubSession) SendSpectate(id int) error { s.lastCall, s.spectate = "spectate", id; return nil }
func (s *stubSession) SendVote(a bool) error     { s.lastCall, s.vote = "vote", a; return nil }
func (s *stubSession) SendCallVote(vt, v, r string) error {
	s.lastCall, s.callVote = "callVote", [3]string{vt, v, r}
	return nil
}

// V18/V20/V22: every Action routes through Do to the session's matching send,
// protocol-independent. This is the single UI-input == ML-output path.
func TestDoDispatch(t *testing.T) {
	s := &stubSession{}
	c := &Client{sess: s}

	cases := []struct {
		act  Action
		want string
	}{
		{ActInput{}, "input"},
		{ActChat{Team: true, Msg: "x"}, "chatTeam"},
		{ActWhisper{ToID: 3, Msg: "y"}, "whisper"},
		{ActEmoticon{Emoticon: packet.EmoticonHearts}, "emoticon"},
		{ActKill{}, "kill"},
		{ActVote{Approve: true}, "vote"},
		{ActCallVote{Type: "kick", Value: "2"}, "callVote"},
		{ActSetTeam{Team: 1}, "setTeam"},
		{ActSetSpectator{TargetID: 5}, "spectate"},
	}
	for _, tc := range cases {
		s.lastCall = ""
		if err := c.Do(tc.act); err != nil {
			t.Errorf("Do(%T) error: %v", tc.act, err)
		}
		if s.lastCall != tc.want {
			t.Errorf("Do(%T): called %q, want %q", tc.act, s.lastCall, tc.want)
		}
	}

	// Payloads carried through.
	_ = c.Do(ActWhisper{ToID: 7, Msg: "hi"})
	if s.whisperT != 7 {
		t.Errorf("whisper target not passed: %d", s.whisperT)
	}
	_ = c.Do(ActSetSpectator{TargetID: 9})
	if s.spectate != 9 {
		t.Errorf("spectate target not passed: %d", s.spectate)
	}
}

// Do without a session returns ErrNotConnected.
func TestDoNotConnected(t *testing.T) {
	c := &Client{}
	if err := c.Do(ActKill{}); err != ErrNotConnected {
		t.Errorf("want ErrNotConnected, got %v", err)
	}
}
