package net7

import (
	"bytes"
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// V59: 0.7 connless getinfo = 9-byte connless header + GETINFO magic + varint token.
func TestBuildInfoRequestConnless(t *testing.T) {
	srv := packet.Token{1, 2, 3, 4}
	cli := packet.Token{5, 6, 7, 8}
	req := BuildInfoRequestConnless(srv, cli, 99)
	if len(req) < HeaderSizeConnless+len(packet.ServerBrowseGetInfo) {
		t.Fatalf("request too short: %d", len(req))
	}
	// Connless header carries both tokens.
	if !bytes.Equal(req[1:5], srv[:]) || !bytes.Equal(req[5:9], cli[:]) {
		t.Errorf("tokens not in header: % x", req[:9])
	}
	if !bytes.Equal(req[9:9+len(packet.ServerBrowseGetInfo)], packet.ServerBrowseGetInfo) {
		t.Errorf("GETINFO magic missing: % x", req[9:])
	}
}

// V59: ParseTokenResponse pulls the server token from a NET_CTRLMSG_TOKEN reply.
func TestParseTokenResponse(t *testing.T) {
	// 7-byte control header + ctrl byte + 4-byte server token.
	reply := []byte{byte((1 << 2) & 0xfc), 0, 0, 0, 0, 0, 0} // control flag, rest ignored
	reply = append(reply, MsgCtrlToken, 0xAA, 0xBB, 0xCC, 0xDD)
	tok, ok := ParseTokenResponse(reply)
	if !ok || tok != (packet.Token{0xAA, 0xBB, 0xCC, 0xDD}) {
		t.Errorf("token = % x ok=%v, want AA BB CC DD", tok, ok)
	}
	if _, ok := ParseTokenResponse([]byte{0, 0, 0}); ok {
		t.Error("short datagram should be rejected")
	}
}

// V59: ConnlessInfoPayload strips header+magic; rejects short/non-inf3.
func TestConnlessInfoPayload(t *testing.T) {
	body := []byte("body7")
	dg := make([]byte, HeaderSizeConnless)
	dg = append(dg, packet.ServerBrowseInfo...)
	dg = append(dg, body...)
	got, ok := ConnlessInfoPayload(dg)
	if !ok || !bytes.Equal(got, body) {
		t.Errorf("payload = %q ok=%v, want %q", got, ok, body)
	}
	if _, ok := ConnlessInfoPayload(make([]byte, HeaderSizeConnless+2)); ok {
		t.Error("non-inf3 datagram should be rejected")
	}
}

// V60: 0.7 info body (varint ints; hostname + skill fields; 0=player) decodes
// to packet.ServerInfo. A spectator (flag 1) yields IsPlayer=false.
func TestParseInfoResponse(t *testing.T) {
	var b []byte
	b = append(b, packer.PackInt(99)...)             // token
	b = append(b, packer.PackStr("0.7")...)          // version
	b = append(b, packer.PackStr("Seven Srv")...)    // name
	b = append(b, packer.PackStr("host.example")...) // hostname (0.7 only)
	b = append(b, packer.PackStr("ctf1")...)         // map
	b = append(b, packer.PackStr("CTF")...)          // gametype
	b = append(b, packer.PackInt(0)...)              // flags (no password)
	b = append(b, packer.PackInt(2)...)              // skill level (0.7 only)
	b = append(b, packer.PackInt(1)...)              // num players
	b = append(b, packer.PackInt(8)...)              // max players
	b = append(b, packer.PackInt(2)...)              // num clients
	b = append(b, packer.PackInt(8)...)              // max clients
	b = append(b, packer.PackStr("bob")...)
	b = append(b, packer.PackStr("")...)
	b = append(b, packer.PackInt(49)...) // country
	b = append(b, packer.PackInt(5)...)  // score
	b = append(b, packer.PackInt(1)...)  // spectator

	info, err := ParseInfoResponse(b)
	if err != nil {
		t.Fatalf("ParseInfoResponse: %v", err)
	}
	if info.Name != "Seven Srv" || info.MapName != "ctf1" || info.GameType != "CTF" || info.Passworded {
		t.Errorf("basics wrong: %+v", info)
	}
	if info.NumPlayers != 1 || info.MaxPlayers != 8 || info.NumClients != 2 || info.MaxClients != 8 {
		t.Errorf("counts wrong: %+v", info)
	}
	if len(info.Clients) != 1 || info.Clients[0].Name != "bob" || info.Clients[0].Country != 49 || info.Clients[0].IsPlayer {
		t.Errorf("client wrong (spectator expected): %+v", info.Clients)
	}
}
