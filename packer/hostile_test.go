package packer

import "testing"

// V70: hostile input to the public decode surface must never panic — empty,
// nil, truncated, and oversized requests return an error or a clamped result.
func TestHostileInputNoPanic(t *testing.T) {
	garbage := [][]byte{
		nil,
		{},
		{0xff},
		{0x80, 0x80, 0x80, 0x80, 0x80}, // never-terminating varint
		{'n', 'o', 'n', 'u', 'l'},      // unterminated string
		make([]byte, 4096),
	}
	for _, b := range garbage {
		u := NewUnpacker(b)
		_, _ = u.GetInt()        // must not panic
		_, _ = u.GetString()     // must not panic
		_, _ = u.GetRaw(1 << 20) // oversized request → error, not panic/OOM read
		_, _ = UnpackInt(b)      // must not panic
	}

	// Pack side: extreme values must not panic.
	_ = PackInt(-1 << 62)
	_ = PackInt(1<<62 - 1)
	_ = PackStr("")
	_ = AppendInt(nil, 0)
}
