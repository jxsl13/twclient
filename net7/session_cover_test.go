package net7

import (
	"log/slog"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// Send-side session API end to end against a local UDP sink — covers each Send*
// plus the message constructors + builders they call transitively (V133).
func TestSessionSendMethods07(t *testing.T) {
	s := newTestSessionLive(t)
	ok := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	ok("SendPacket", s.SendPacket([]byte{0x00}))
	ok("SendCtrl", s.SendCtrl(CtrlKeepAlive()))
	ok("SendResendRequest", s.SendResendRequest())
	ok("SendKeepAlive", s.SendKeepAlive())
	ok("SendVitalMsg", s.SendVitalMsg(SysReady()))
	ok("SendNonVitalMsg", s.SendNonVitalMsg(SysPing()))
	ok("SendInput", s.SendInput(100, 101, packet.EmptyInputSize, packet.EmptyInput))
	ok("SendChat", s.SendChat("hi"))
	ok("SendChatTeam", s.SendChatTeam(true, "team"))
	ok("SendWhisper", s.SendWhisper(2, "psst"))
	ok("SendKill", s.SendKill())
	ok("SendRconAuth", s.SendRconAuth("pw"))
	ok("SendRconCmd", s.SendRconCmd("status"))
	ok("SendEmoticon", s.SendEmoticon(packet.EmoticonHearts))
	ok("SendSetTeam", s.SendSetTeam(0))
	ok("SendSpectate", s.SendSpectate(2))
	ok("SendVote", s.SendVote(true))
	ok("SendCallVote", s.SendCallVote("kick", "3", "afk"))
}

func TestSessionAccessors07(t *testing.T) {
	s := newTestSessionLive(t)
	_ = s.LocalID()
	_ = s.Capabilities()
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

func TestSessionOptions07(t *testing.T) {
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

// SnapItemSize over every known 0.7 object type + the unknown default (V133).
func TestSnapItemSize07(t *testing.T) {
	known := []int{
		ObjPlayerInput, ObjProjectile, ObjLaser, ObjPickup, ObjFlag,
		ObjGameData, ObjGameDataTeam, ObjGameDataFlag, ObjCharacterCore, ObjCharacter,
		ObjPlayerInfo, ObjSpectatorInfo, ObjDeClientInfo, ObjDeGameInfo, ObjDeTuneParams,
		ObjCommon, ObjExplosion, ObjSpawn, ObjHammerHit, ObjDeath, ObjSoundWorld, ObjDamage,
	}
	for _, id := range known {
		if SnapItemSize(id) <= 0 {
			t.Errorf("SnapItemSize(%d) <= 0", id)
		}
	}
	if SnapItemSize(-99999) != -1 {
		t.Error("SnapItemSize(unknown) != -1")
	}
}

// Packet builders (pure constructors) + the token-handshake builders (V133).
func TestPacketBuilders07(t *testing.T) {
	var tok packet.Token
	builders := map[string][]byte{
		"BuildCtrlPacket":      BuildCtrlPacket(tok, 0, []byte{0x00}),
		"BuildTokenRequest":    BuildTokenRequest(tok),
		"BuildConnect":         BuildConnect(tok, tok),
		"BuildKeepAlive":       BuildKeepAlive(tok, 0),
		"BuildClose":           BuildClose(tok, 0, "bye"),
		"BuildChunkPacket":     BuildChunkPacket(tok, 0, 1, false, WrapVitalChunk(SysReady(), 1)),
		"BuildInfoPacket":      BuildInfoPacket(tok, 0, 1),
		"BuildReadyPacket":     BuildReadyPacket(tok, 0, 2),
		"BuildEnterGamePacket": BuildEnterGamePacket(tok, 0, 3),
	}
	for name, b := range builders {
		if len(b) == 0 {
			t.Errorf("%s produced empty bytes", name)
		}
	}
}

// Header Pack/Unpack round-trip across the flag variants (V133).
func TestHeaderRoundTrip07(t *testing.T) {
	cases := []Header{
		{Ack: 5, NumChunks: 2},
		{Flags: Flags{Compression: true}, Ack: 7, NumChunks: 1},
		{Flags: Flags{Control: true}},
		{Flags: Flags{Resend: true}, Ack: 9, NumChunks: 1},
	}
	for i, h := range cases {
		packed := h.Pack()
		if len(packed) == 0 {
			t.Fatalf("case %d: Pack empty", i)
		}
		var got Header
		if err := got.Unpack(packed); err != nil {
			t.Errorf("case %d: Unpack: %v", i, err)
		}
	}
}
