package packet

import (
	"bytes"
	"testing"

	"github.com/jxsl13/twclient/packer"
)

// sysExChunk builds a non-vital chunk carrying a NETMSG_EX system message
// (msg id 0, sys flag) with the given UUID+body bytes. split=6 (net6).
func sysExChunk(body []byte) []byte {
	data := append(packer.PackInt(1), body...) // msgRaw=1 → sys, id 0
	size := len(data)
	// non-vital 2-byte chunk header (flags=0): size split across 6 bits.
	hdr := []byte{byte((size >> 6) & 0x3F), byte(size & 0x3F)}
	return append(hdr, data...)
}

// V47 regression: ExtractAllSysMsgPayloads must return EVERY NETMSG_EX in a
// packet, not just the first — DDNet sends capabilities alongside other EX
// messages before MAP_CHANGE, so stopping at the first one drops capabilities.
func TestExtractAllSysMsgPayloads(t *testing.T) {
	a := []byte("AAAAAAAAAAAAAAAA01") // 16-byte uuid + 2-byte body
	b := []byte("BBBBBBBBBBBBBBBB99")
	payload := append(sysExChunk(a), sysExChunk(b)...)

	got := ExtractAllSysMsgPayloads(payload, 0, 6)
	if len(got) != 2 {
		t.Fatalf("want 2 EX payloads, got %d", len(got))
	}
	if !bytes.Equal(got[0], a) || !bytes.Equal(got[1], b) {
		t.Errorf("payloads mismatch: %q %q", got[0], got[1])
	}

	// The single-match helper returns only the first.
	if first := ExtractSysMsgPayload(payload, 0, 6); !bytes.Equal(first, a) {
		t.Errorf("ExtractSysMsgPayload first = %q, want %q", first, a)
	}
}
