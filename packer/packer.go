// Package packer wraps the Teeworlds varint encoding and provides
// string/raw/message-ID packing helpers used in both 0.6 and 0.7 protocols.
package packer

import (
	"errors"
	"fmt"

	"github.com/teeworlds-go/varint"
)

// Unpacker reads packed data from a byte buffer.
type Unpacker struct {
	data []byte
	idx  int
}

// NewUnpacker creates an Unpacker from the given data.
func NewUnpacker(data []byte) *Unpacker {
	buf := make([]byte, len(data))
	copy(buf, data)
	return &Unpacker{data: buf}
}

// Reset resets the unpacker with new data.
func (u *Unpacker) Reset(data []byte) {
	u.data = make([]byte, len(data))
	copy(u.data, data)
	u.idx = 0
}

// Size returns the total buffer size.
func (u *Unpacker) Size() int { return len(u.data) }

// RemainingSize returns the number of unread bytes.
func (u *Unpacker) RemainingSize() int { return len(u.data) - u.idx }

// Data returns the full buffer.
func (u *Unpacker) Data() []byte { return u.data }

// RemainingData returns unread data without consuming it.
func (u *Unpacker) RemainingData() []byte { return u.data[u.idx:] }

// Rest consumes and returns all remaining data.
func (u *Unpacker) Rest() []byte {
	rest := u.data[u.idx:]
	u.idx = len(u.data)
	return rest
}

// GetByte consumes and returns a single byte.
func (u *Unpacker) GetByte() (byte, error) {
	if u.RemainingSize() < 1 {
		return 0, errors.New("packer: not enough data for GetByte")
	}
	b := u.data[u.idx]
	u.idx++
	return b, nil
}

// GetRaw consumes and returns size bytes.
func (u *Unpacker) GetRaw(size int) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("packer: GetRaw negative size %d", size)
	}
	if u.idx+size > len(u.data) {
		return nil, fmt.Errorf("packer: GetRaw needs %d bytes, only %d available", size, u.RemainingSize())
	}
	b := u.data[u.idx : u.idx+size]
	u.idx += size
	return b, nil
}

// GetInt unpacks a Teeworlds variable-length integer using the varint library.
func (u *Unpacker) GetInt() (int, error) {
	if u.RemainingSize() < 1 {
		return 0, errors.New("packer: not enough data for GetInt")
	}
	val, n := varint.Varint(u.data[u.idx:])
	if n <= 0 {
		return 0, errors.New("packer: failed to unpack int")
	}
	u.idx += n
	return val, nil
}

// GetString unpacks a null-terminated string with basic sanitization.
func (u *Unpacker) GetString() (string, error) {
	return u.GetStringSanitized(SanitizeDefault)
}

// Sanitization flags.
const (
	SanitizeDefault         = 1
	SanitizeCC              = 2
	SanitizeSkipWhitespaces = 4
)

// GetStringSanitized unpacks a null-terminated string with the given sanitization.
func (u *Unpacker) GetStringSanitized(flags int) (string, error) {
	var buf []byte
	skipping := flags&SanitizeSkipWhitespaces != 0

	for {
		b, err := u.GetByte()
		if err != nil {
			return "", fmt.Errorf("packer: unterminated string: %w", err)
		}
		if b == 0 {
			break
		}
		if skipping {
			if b == ' ' || b == '\t' || b == '\n' {
				continue
			}
			skipping = false
		}
		if flags&SanitizeCC != 0 {
			if b < 32 {
				b = ' '
			}
		} else if flags&SanitizeDefault != 0 {
			if b < 32 && b != '\r' && b != '\n' && b != '\t' {
				b = ' '
			}
		}
		buf = append(buf, b)
	}
	return string(buf), nil
}

// GetMsgAndSys unpacks a message ID and its system flag.
// The last bit of the packed int determines system (1) vs game (0).
func (u *Unpacker) GetMsgAndSys() (msgID int, system bool, err error) {
	raw, err := u.GetInt()
	if err != nil {
		return 0, false, err
	}
	return raw >> 1, raw&1 != 0, nil
}

// --- Packing functions (delegating to varint library) ---

// PackInt packs an integer using teeworlds variable-length encoding.
// Values are clamped to 32-bit range [-2147483648, 2147483647].
func PackInt(num int) []byte {
	if num > 0x7FFFFFFF {
		num = 0x7FFFFFFF
	} else if num < -0x80000000 {
		num = -0x80000000
	}
	return varint.AppendVarint(nil, num)
}

// PackStr packs a string as null-terminated bytes.
func PackStr(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// PackBool packs a boolean as a single packed int (0 or 1).
func PackBool(v bool) []byte {
	if v {
		return PackInt(1)
	}
	return PackInt(0)
}

// PackMsgID packs a message ID with the system flag in the last bit.
func PackMsgID(msgID int, system bool) []byte {
	id := msgID << 1
	if system {
		id |= 1
	}
	return PackInt(id)
}

// UnpackInt unpacks a single integer from a byte slice (stateless helper).
func UnpackInt(data []byte) (int, error) {
	val, n := varint.Varint(data)
	if n <= 0 {
		return 0, errors.New("packer: failed to unpack int")
	}
	return val, nil
}
