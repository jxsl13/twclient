package net7

import (
	"fmt"

	"github.com/jxsl13/twclient/packet"
)

// Header represents the 0.7 packet header (always 7 bytes).
//
// Layout:
//
//	--ff ffaa  aaaa aaaa  cccc cccc  tttt tttt  tttt tttt  tttt tttt  tttt tttt
//	unused(2) flags(4) ack(10) numchunks(8) token(32)
//
// Flags order: connless, compression, resend, control
type Header struct {
	Flags     Flags
	Ack       int
	NumChunks int
	Token     packet.Token

	// Only for connless packets
	ResponseToken packet.Token
}

// Flags represents 0.7 packet flags.
type Flags struct {
	Connless    bool
	Compression bool
	Resend      bool
	Control     bool
}

// MaxPayloadSize is the maximum payload size for 0.7 (header is always 6 bytes for connected).
const MaxPayloadSize = packet.MaxPacketSize - 6 // 1394

const (
	flagControl     = 1
	flagResend      = 2
	flagCompression = 4
	flagConnless    = 8
)

// HeaderSize is the fixed header size for 0.7.
const HeaderSize = 7

// HeaderSizeConnless is the header size for connless 0.7 packets.
const HeaderSizeConnless = 9

// Pack serializes the 0.7 header.
func (h *Header) Pack() []byte {
	if h.Flags.Connless {
		version := 1
		return append(
			[]byte{byte(((flagConnless << 2) & 0xFC) | (version & 0x03))},
			append(h.Token[:], h.ResponseToken[:]...)...,
		)
	}

	flags := 0
	if h.Flags.Control {
		flags |= flagControl
	}
	if h.Flags.Resend {
		flags |= flagResend
	}
	if h.Flags.Compression {
		flags |= flagCompression
	}

	data := make([]byte, HeaderSize)
	data[0] = byte(((flags << 2) & 0xFC) | ((h.Ack >> 8) & 0x03))
	data[1] = byte(h.Ack & 0xFF)
	data[2] = byte(h.NumChunks)
	copy(data[3:7], h.Token[:])
	return data
}

// Unpack deserializes a 0.7 header from raw bytes.
func (h *Header) Unpack(data []byte) error {
	if len(data) < HeaderSize {
		return fmt.Errorf("packet07: header too short: %d bytes", len(data))
	}
	if err := h.Flags.Unpack(data); err != nil {
		return err
	}
	h.Ack = (int(data[0]&0x03) << 8) | int(data[1])
	h.NumChunks = int(data[2])
	copy(h.Token[:], data[3:7])
	return nil
}

// Unpack parses 0.7 flags from the first byte.
func (f *Flags) Unpack(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("packet07: empty header for flags")
	}
	bits := data[0] >> 2
	f.Control = bits&flagControl != 0
	f.Resend = bits&flagResend != 0
	f.Compression = bits&flagCompression != 0
	f.Connless = bits&flagConnless != 0
	return nil
}
