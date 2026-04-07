package packet

import (
	"fmt"

	"github.com/jxsl13/tw-protocol/packer"
)

const (
	chunkFlagVital  = 1
	chunkFlagResend = 2
)

// ChunkFlags represents chunk header flags.
type ChunkFlags struct {
	Vital  bool
	Resend bool
}

func (f ChunkFlags) bits() int {
	v := 0
	if f.Resend {
		v |= chunkFlagResend
	}
	if f.Vital {
		v |= chunkFlagVital
	}
	return v
}

// ChunkHeader represents a chunk header.
// Non-vital: 2 bytes (flags + size).
// Vital: 3 bytes (flags + size + sequence number).
type ChunkHeader struct {
	Flags ChunkFlags
	Size  int // payload size in bytes (includes message ID, excludes chunk header)
	Seq   int // sequence number (only for vital chunks)
}

// HeaderSize returns the byte size of this chunk header (2 or 3).
func (h *ChunkHeader) HeaderSize() int {
	if h.Flags.Vital {
		return 3
	}
	return 2
}

// Pack serializes the chunk header using the given split parameter.
//
// The split controls how size and sequence bits are distributed in byte 1:
//   - Split=4 (0.6): size uses 6+4=10 bits, seq uses 4+8=12 bits
//   - Split=6 (0.7): size uses 6+6=12 bits, seq uses 2+8=10 bits
//
// Layout:
//
//	byte 0: [FF SSSSSS] F=flags(2bit), S=size high 6 bits
//	byte 1: [QQ..SSSS] Q=seq high bits, S=size low (split) bits
//	byte 2: [QQQQQQQQ] Q=seq low 8 bits (vital only)
func (h *ChunkHeader) Pack(split int) []byte {
	sizeLowMask := (1 << split) - 1
	data := make([]byte, h.HeaderSize())
	data[0] = (byte(h.Flags.bits()&0x03) << 6) | byte((h.Size>>split)&0x3F)
	data[1] = byte(h.Size & sizeLowMask)
	if h.Flags.Vital {
		data[1] |= byte((h.Seq >> 2) & ^sizeLowMask & 0xFF)
		data[2] = byte(h.Seq & 0xFF)
	}
	return data
}

// Unpack deserializes a chunk header from the unpacker.
func (h *ChunkHeader) Unpack(u *packer.Unpacker, split int) error {
	sizeLowMask := (1 << split) - 1
	raw, err := u.GetRaw(2)
	if err != nil {
		return fmt.Errorf("chunk header: %w", err)
	}
	flagBits := (raw[0] >> 6) & 0x03
	h.Flags.Vital = flagBits&chunkFlagVital != 0
	h.Flags.Resend = flagBits&chunkFlagResend != 0
	h.Size = (int(raw[0]&0x3F) << split) | int(raw[1])&sizeLowMask

	if h.Flags.Vital {
		b, err := u.GetByte()
		if err != nil {
			return fmt.Errorf("chunk header vital seq: %w", err)
		}
		h.Seq = (int(raw[1]) & ^sizeLowMask & 0xFF)<<2 | int(b)
	}
	return nil
}

// UnpackRaw deserializes a chunk header from raw bytes.
func (h *ChunkHeader) UnpackRaw(data []byte, split int) error {
	sizeLowMask := (1 << split) - 1
	if len(data) < 2 {
		return fmt.Errorf("chunk header: need at least 2 bytes, got %d", len(data))
	}
	flagBits := (data[0] >> 6) & 0x03
	h.Flags.Vital = flagBits&chunkFlagVital != 0
	h.Flags.Resend = flagBits&chunkFlagResend != 0
	h.Size = (int(data[0]&0x3F) << split) | int(data[1])&sizeLowMask
	if h.Flags.Vital {
		if len(data) < 3 {
			return fmt.Errorf("chunk header: need 3 bytes for vital, got %d", len(data))
		}
		h.Seq = (int(data[1]) & ^sizeLowMask & 0xFF)<<2 | int(data[2])
	}
	return nil
}

// Chunk is a chunk header plus its payload data.
type Chunk struct {
	Header ChunkHeader
	Data   []byte
}

// UnpackChunks splits a packet payload into individual chunks.
func UnpackChunks(payload []byte, split int) []Chunk {
	u := packer.NewUnpacker(payload)
	var chunks []Chunk

	for u.RemainingSize() > 0 {
		var hdr ChunkHeader
		if err := hdr.Unpack(u, split); err != nil {
			break
		}
		if hdr.Size <= 0 || hdr.Size > u.RemainingSize() {
			data := u.Rest()
			if len(data) > 0 {
				chunks = append(chunks, Chunk{Header: hdr, Data: data})
			}
			break
		}
		data, err := u.GetRaw(hdr.Size)
		if err != nil {
			break
		}
		chunks = append(chunks, Chunk{Header: hdr, Data: data})
	}
	return chunks
}
