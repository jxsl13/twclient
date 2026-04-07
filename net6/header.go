package net6

import (
	"fmt"

	"github.com/jxsl13/tw-protocol/packet"
)

// Header represents the 0.6 packet header.
//
// 0.6.4 layout (3 bytes, no token):
//
//	ffff --aa  aaaa aaaa  cccc cccc
//
// 0.6.5 layout (7 bytes, with token flag):
//
//	ffff f-aa  aaaa aaaa  cccc cccc  tttt tttt  tttt tttt  tttt tttt  tttt tttt
//
// Flags order: compression, resend, connless, control, token
type Header struct {
	Flags     Flags
	Ack       int
	NumChunks int
	Token     packet.Token
}

// Flags represents 0.6 packet flags.
type Flags struct {
	Compression bool
	Resend      bool
	Connless    bool
	Control     bool
	Token       bool // 0.6.5 only
}

// MaxPayloadSize is the maximum payload size for 0.6 (header can be up to 10 bytes with token).
const MaxPayloadSize = packet.MaxPacketSize - 10 // 1390

const (
	flagCompression = 1 << 7 // bit 7
	flagResend      = 1 << 6 // bit 6
	flagConnless    = 1 << 5 // bit 5
	flagControl     = 1 << 4 // bit 4
	flagToken       = 1 << 3 // bit 3 (0.6.5+)
)

// Size returns the header size (3 without token, 7 with token).
func (h *Header) Size() int {
	if h.Flags.Token {
		return 7
	}
	return 3
}

// Pack serializes the 0.6 header.
func (h *Header) Pack() []byte {
	var flags byte
	if h.Flags.Compression {
		flags |= flagCompression
	}
	if h.Flags.Resend {
		flags |= flagResend
	}
	if h.Flags.Connless {
		flags |= flagConnless
	}
	if h.Flags.Control {
		flags |= flagControl
	}
	if h.Flags.Token {
		flags |= flagToken
	}

	b0 := (flags & 0xFC) | byte((h.Ack>>8)&0x03)
	b1 := byte(h.Ack & 0xFF)
	b2 := byte(h.NumChunks)

	if h.Flags.Token {
		data := make([]byte, 7)
		data[0] = b0
		data[1] = b1
		data[2] = b2
		copy(data[3:7], h.Token[:])
		return data
	}
	return []byte{b0, b1, b2}
}

// Unpack deserializes a 0.6 header from raw bytes.
func (h *Header) Unpack(data []byte) error {
	if len(data) < 3 {
		return fmt.Errorf("packet06: header too short: %d bytes", len(data))
	}
	h.Flags.Unpack(data[0])
	h.Ack = (int(data[0]&0x03) << 8) | int(data[1])
	h.NumChunks = int(data[2])

	if h.Flags.Token {
		if len(data) < 7 {
			return fmt.Errorf("packet06: token flag set but header too short: %d bytes", len(data))
		}
		copy(h.Token[:], data[3:7])
	}
	return nil
}

// Unpack parses 0.6 flags from the first byte.
func (f *Flags) Unpack(b byte) {
	f.Compression = b&flagCompression != 0
	f.Resend = b&flagResend != 0
	f.Connless = b&flagConnless != 0
	f.Control = b&flagControl != 0
	f.Token = b&flagToken != 0
}
