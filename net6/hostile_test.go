package net6

import "testing"

// V70: nil options and garbage parse input must not panic.
func TestHostileInputNoPanic(t *testing.T) {
	s, err := NewSession("127.0.0.1:34999", nil) // nil option ignored
	if err != nil {
		t.Fatalf("NewSession with nil option: %v", err)
	}
	defer s.Close()

	for _, b := range [][]byte{nil, {}, {0xff}, make([]byte, 5), make([]byte, 2048)} {
		_, _ = ConnlessInfoPayload(b)
		_, _ = ParseInfoResponse(b)
		var h Header
		_ = h.Unpack(b)
	}
	_ = BuildInfoRequestConnless(0) // build never panics
}
