package net6

import "testing"

// strToInts mirrors DDNet StrToInts: pack 4 chars per int, each byte +128, and
// clear the low bit of the last int (sanitization). Used to build test fixtures.
func strToInts(s string, n int) []int {
	b := []byte(s)
	out := make([]int, n)
	bi := 0
	for i := range n {
		var v int
		for range 4 {
			ch := 0
			if bi < len(b) {
				ch = int(b[bi])
				bi++
			}
			v = (v << 8) | ((ch + 128) & 0xff)
		}
		out[i] = v
	}
	out[n-1] &^= 1
	return out
}

func TestIntsToStrRoundTrip(t *testing.T) {
	cases := []struct {
		s string
		n int
	}{
		{"abc", 4},
		{"nameless tee", 4}, // 12 chars, fits 4 ints (16)
		{"DDNet", 3},
		{"default", 6},
		{"", 4},
	}
	for _, tc := range cases {
		got := IntsToStr(strToInts(tc.s, tc.n))
		if got != tc.s {
			t.Errorf("round-trip %q: got %q", tc.s, got)
		}
	}
}

func TestDecodeClientInfo(t *testing.T) {
	fields := make([]int, SizeClientInfo)
	copy(fields[0:4], strToInts("nameless tee", 4))
	copy(fields[4:7], strToInts("DDNet", 3))
	fields[7] = 276
	copy(fields[8:14], strToInts("default", 6))

	ci := DecodeClientInfo(fields)
	if ci.Name != "nameless tee" || ci.Clan != "DDNet" || ci.Country != 276 || ci.Skin != "default" {
		t.Fatalf("decode: %+v", ci)
	}
}

func TestDecodeClientInfoShort(t *testing.T) {
	// A short field slice must yield the zero value, not panic (V70/V96).
	if ci := DecodeClientInfo([]int{1, 2, 3}); ci != (ClientInfo{}) {
		t.Fatalf("short decode should be zero, got %+v", ci)
	}
}
