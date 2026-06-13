// Package packer wraps the Teeworlds varint encoding and provides
// string/raw/message-ID packing helpers used in both 0.6 and 0.7 protocols.
package packer

import (
	"bytes"
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

// Reset resets the unpacker with a private copy of data. The internal buffer is
// reused across resets (only its contents change), so a long-lived/pooled
// Unpacker allocates at most once it has grown to the largest payload seen.
// The unpacker only reads, never writes back, so a copy is fully equivalent to
// the original — it just keeps the caller free to reuse/mutate its own buffer.
func (u *Unpacker) Reset(data []byte) {
	u.data = append(u.data[:0], data...)
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

// NextByte consumes and returns a single byte.
func (u *Unpacker) NextByte() (byte, error) {
	if u.RemainingSize() < 1 {
		return 0, errors.New("packer: not enough data for NextByte")
	}
	b := u.data[u.idx]
	u.idx++
	return b, nil
}

// NextRaw consumes and returns size bytes.
func (u *Unpacker) NextRaw(size int) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("packer: NextRaw negative size %d", size)
	}
	if u.idx+size > len(u.data) {
		return nil, fmt.Errorf("packer: NextRaw needs %d bytes, only %d available", size, u.RemainingSize())
	}
	b := u.data[u.idx : u.idx+size]
	u.idx += size
	return b, nil
}

// NextInt unpacks a Teeworlds variable-length integer using the varint library.
func (u *Unpacker) NextInt() (int, error) {
	if u.RemainingSize() < 1 {
		return 0, errors.New("packer: not enough data for NextInt")
	}
	val, n := varint.Varint(u.data[u.idx:])
	if n <= 0 {
		return 0, errors.New("packer: failed to unpack int")
	}
	u.idx += n
	return val, nil
}

// NextString unpacks a null-terminated string with basic sanitization.
func (u *Unpacker) NextString() (string, error) {
	return u.NextStringSanitized(SanitizeDefault)
}

// Sanitization flags.
const (
	SanitizeDefault         = 1
	SanitizeCC              = 2
	SanitizeSkipWhitespaces = 4
)

// NextStringSanitized unpacks a null-terminated string with the given sanitization.
func (u *Unpacker) NextStringSanitized(flags int) (string, error) {
	rem := u.data[u.idx:]
	nul := bytes.IndexByte(rem, 0)
	if nul < 0 {
		return "", errors.New("packer: unterminated string")
	}
	raw := rem[:nul]
	u.idx += nul + 1 // consume the string and its NUL terminator

	// Fast path: most strings need no sanitization. Scan once; if nothing
	// would change, convert the slice directly (a single allocation) instead
	// of rebuilding it byte-by-byte.
	skipping := flags&SanitizeSkipWhitespaces != 0
	needsWork := skipping
	if !needsWork {
		for _, b := range raw {
			if b < 32 {
				if flags&SanitizeCC != 0 {
					needsWork = true
					break
				}
				if flags&SanitizeDefault != 0 && b != '\r' && b != '\n' && b != '\t' {
					needsWork = true
					break
				}
			}
		}
	}
	if !needsWork {
		return string(raw), nil
	}

	buf := make([]byte, 0, len(raw))
	for _, b := range raw {
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

// NextMsgAndSys unpacks a message ID and its system flag.
// The last bit of the packed int determines system (1) vs game (0).
func (u *Unpacker) NextMsgAndSys() (msgID int, system bool, err error) {
	raw, err := u.NextInt()
	if err != nil {
		return 0, false, err
	}
	return raw >> 1, raw&1 != 0, nil
}

// --- Packing functions (delegating to varint library) ---

// AppendInt appends a teeworlds variable-length integer to dst and returns the
// extended slice. Values are clamped to 32-bit range [-2147483648,
// 2147483647]. Appending into a reused buffer avoids the per-field allocation
// of PackInt on hot build paths.
func AppendInt(dst []byte, num int) []byte {
	if num > 0x7FFFFFFF {
		num = 0x7FFFFFFF
	} else if num < -0x80000000 {
		num = -0x80000000
	}
	return varint.AppendVarint(dst, num)
}

// AppendStr appends a null-terminated string to dst and returns the extended slice.
func AppendStr(dst []byte, s string) []byte {
	dst = append(dst, s...)
	return append(dst, 0)
}

// AppendBool appends a boolean as a single packed int (0 or 1) to dst.
func AppendBool(dst []byte, v bool) []byte {
	if v {
		return AppendInt(dst, 1)
	}
	return AppendInt(dst, 0)
}

// AppendMsgID appends a message ID with the system flag in the last bit to dst.
func AppendMsgID(dst []byte, msgID int, system bool) []byte {
	id := msgID << 1
	if system {
		id |= 1
	}
	return AppendInt(dst, id)
}

// PackInt packs an integer using teeworlds variable-length encoding.
// Values are clamped to 32-bit range [-2147483648, 2147483647].
func PackInt(num int) []byte { return AppendInt(nil, num) }

// PackStr packs a string as null-terminated bytes.
func PackStr(s string) []byte { return AppendStr(nil, s) }

// PackBool packs a boolean as a single packed int (0 or 1).
func PackBool(v bool) []byte { return AppendBool(nil, v) }

// PackMsgID packs a message ID with the system flag in the last bit.
func PackMsgID(msgID int, system bool) []byte { return AppendMsgID(nil, msgID, system) }

// UnpackInt unpacks a single integer from a byte slice (stateless helper).
func UnpackInt(data []byte) (int, error) {
	val, n := varint.Varint(data)
	if n <= 0 {
		return 0, errors.New("packer: failed to unpack int")
	}
	return val, nil
}
