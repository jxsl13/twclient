package net6

import (
	"log/slog"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// Exercises the send-side session API end to end against a local (unconnected)
// UDP conn: each Send* builds its message + transmits fire-and-forget, which also
// covers the underlying message constructors (GameClSay/SysRconAuth/…) and packet
// builders transitively (V133). No server needed.
func TestSessionSendMethods(t *testing.T) {
	s := newTestSessionLive(t)

	mustOK := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	mustOK("SendPacket", s.SendPacket([]byte{0x00}))
	mustOK("SendCtrl", s.SendCtrl(CtrlKeepAlive()))
	mustOK("SendKeepAlive", s.SendKeepAlive())
	mustOK("SendVitalMsg", s.SendVitalMsg(SysReady()))
	mustOK("SendNonVitalMsg", s.SendNonVitalMsg(SysPingReply()))
	mustOK("SendInput", s.SendInput(100, 101, packet.EmptyInputSize, packet.EmptyInput))
	mustOK("SendChat", s.SendChat("hi"))
	mustOK("SendChatTeam", s.SendChatTeam(true, "team"))
	mustOK("SendWhisper", s.SendWhisper(3, "psst"))
	mustOK("SendKill", s.SendKill())
	mustOK("SendRconAuth", s.SendRconAuth("pw"))
	mustOK("SendRconCmd", s.SendRconCmd("status"))
	mustOK("SendEmoticon", s.SendEmoticon(packet.EmoticonHearts))
	mustOK("SendSetTeam", s.SendSetTeam(0))
	mustOK("SendSpectate", s.SendSpectate(2))
	mustOK("SendVote", s.SendVote(true))
	mustOK("SendCallVote", s.SendCallVote("kick", "3", "afk"))
}

// Accessors + Poll (Poll blocks until an event, so emit one first).
func TestSessionAccessors(t *testing.T) {
	s := newTestSessionLive(t)

	if s.LocalID() != -1 {
		t.Errorf("LocalID = %d, want -1", s.LocalID())
	}
	_ = s.Capabilities()
	_ = s.HasSecurityToken()
	_ = s.MapName()
	_ = s.MapInfo()
	_ = s.Map()
	_ = s.LastRecvTime()
	if s.LastSnapTick() != 0 {
		t.Errorf("LastSnapTick = %d, want 0", s.LastSnapTick())
	}
	if s.EventCh() == nil {
		t.Error("EventCh nil")
	}

	s.emit(packet.EventMotd{Text: "x"})
	if ev, err := s.Poll(); err != nil || ev == nil {
		t.Errorf("Poll = %v, %v", ev, err)
	}
}

// Construction options (WithLogger/WithMapCache/size+buffer opts) applied via
// NewSession (which dials a local UDP socket — no server contacted).
func TestSessionOptions(t *testing.T) {
	s, err := NewSession("127.0.0.1:65535",
		WithLogger(slog.New(slog.DiscardHandler)),
		WithMapCache(packet.NewMapCache()),
		WithSnapStorageSize(8),
		WithEventChanSize(32),
		WithReadBufferSize(1<<20),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
}
