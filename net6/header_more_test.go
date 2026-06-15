package net6

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// Header Pack/Unpack round-trips across the flag variants (compression, control,
// connless, token) and the Size accessor (V133).
func TestHeaderRoundTrip(t *testing.T) {
	cases := []Header{
		{Ack: 5, NumChunks: 2},
		{Flags: Flags{Compression: true}, Ack: 7, NumChunks: 1},
		{Flags: Flags{Control: true}, Ack: 0, NumChunks: 0},
		{Flags: Flags{Token: true}, Ack: 3, NumChunks: 1, Token: packet.Token{1, 2, 3, 4}},
		{Flags: Flags{Resend: true}, Ack: 9, NumChunks: 1},
	}
	for i, h := range cases {
		packed := h.Pack()
		if len(packed) == 0 {
			t.Fatalf("case %d: Pack empty", i)
		}
		if h.Size() <= 0 {
			t.Errorf("case %d: Size() = %d", i, h.Size())
		}
		var got Header
		if err := got.Unpack(packed); err != nil {
			t.Errorf("case %d: Unpack: %v", i, err)
			continue
		}
		if got.Ack != h.Ack || got.NumChunks != h.NumChunks {
			t.Errorf("case %d: round-trip ack/num = %d/%d, want %d/%d", i, got.Ack, got.NumChunks, h.Ack, h.NumChunks)
		}
		if got.Flags.Control != h.Flags.Control || got.Flags.Compression != h.Flags.Compression {
			t.Errorf("case %d: flags mismatch: %+v vs %+v", i, got.Flags, h.Flags)
		}
	}

	// Unpack rejects a too-short buffer.
	var h Header
	if err := h.Unpack([]byte{0x00}); err == nil {
		t.Error("Unpack(short) = nil error, want error")
	}
}
