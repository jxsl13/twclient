package net6

import (
	"bytes"
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// V59: 0.6 connless getinfo framing = 6×0xFF + GETINFO magic + token byte.
func TestBuildInfoRequestConnless(t *testing.T) {
	req := BuildInfoRequestConnless(0x42)
	want := append(bytes.Repeat([]byte{0xff}, 6), packet.ServerBrowseGetInfo...)
	want = append(want, 0x42)
	if !bytes.Equal(req, want) {
		t.Errorf("getinfo bytes = % x, want % x", req, want)
	}
}

// V59: ConnlessInfoPayload strips prefix+magic and round-trips the body; rejects
// short/non-inf3 datagrams.
func TestConnlessInfoPayload(t *testing.T) {
	body := []byte("hello-body")
	dg := append(bytes.Repeat([]byte{0xff}, 6), packet.ServerBrowseInfo...)
	dg = append(dg, body...)

	got, ok := ConnlessInfoPayload(dg)
	if !ok || !bytes.Equal(got, body) {
		t.Errorf("payload = %q ok=%v, want %q", got, ok, body)
	}

	if _, ok := ConnlessInfoPayload([]byte{0xff, 0xff}); ok {
		t.Error("short datagram should be rejected")
	}
	bad := append(bytes.Repeat([]byte{0xff}, 6), packet.ServerBrowseGetInfo...) // wrong magic
	if _, ok := ConnlessInfoPayload(bad); ok {
		t.Error("non-inf3 datagram should be rejected")
	}
}

// V60: 0.6 info body (decimal-string ints) decodes to packet.ServerInfo incl.
// the player list, password flag, and derived counts.
func TestParseInfoResponse(t *testing.T) {
	var b []byte
	b = append(b, packer.PackString("12")...)        // token
	b = append(b, packer.PackString("0.6.5")...)     // version
	b = append(b, packer.PackString("My Server")...) // name
	b = append(b, packer.PackString("dm1")...)       // map
	b = append(b, packer.PackString("DM")...)        // gametype
	b = append(b, packer.PackString("1")...)         // flags (password)
	b = append(b, packer.PackString("2")...)         // num players
	b = append(b, packer.PackString("16")...)        // max players
	b = append(b, packer.PackString("3")...)         // num clients
	b = append(b, packer.PackString("16")...)        // max clients
	b = append(b, packer.PackString("alice")...)
	b = append(b, packer.PackString("ACL")...)
	b = append(b, packer.PackString("-1")...) // country
	b = append(b, packer.PackString("10")...) // score
	b = append(b, packer.PackString("1")...)  // is player

	info, err := ParseInfoResponse(b)
	if err != nil {
		t.Fatalf("ParseInfoResponse: %v", err)
	}
	if info.Name != "My Server" || info.MapName != "dm1" || info.GameType != "DM" || !info.Passworded {
		t.Errorf("basics wrong: %+v", info)
	}
	if info.NumPlayers != 2 || info.MaxPlayers != 16 || info.NumClients != 3 || info.MaxClients != 16 {
		t.Errorf("counts wrong: %+v", info)
	}
	if len(info.Clients) != 1 || info.Clients[0].Name != "alice" || info.Clients[0].Country != -1 || !info.Clients[0].IsPlayer {
		t.Errorf("client wrong: %+v", info.Clients)
	}
}
