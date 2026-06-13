package net7

import (
	"bytes"
	"testing"

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
