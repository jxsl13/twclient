package net6

import (
	"bytes"
	"testing"

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
