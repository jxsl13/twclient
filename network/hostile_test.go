package network

import "testing"

// V70: a malformed address errors (⊥ panic); nil options are ignored; a
// negative buffer size clamps to the default.
func TestHostileInputNoPanic(t *testing.T) {
	if _, err := Dial("not a valid address"); err == nil {
		t.Error("malformed address should return an error")
	}
	c, err := Dial("127.0.0.1:34999", nil, WithReadBufferSize(-1))
	if err != nil {
		t.Fatalf("Dial with nil option + negative buffer: %v", err)
	}
	defer c.Close()
	if c.readBufferSize != DefaultReadBufferSize {
		t.Errorf("negative buffer should clamp to default, got %d", c.readBufferSize)
	}
	if c.ReadTimeout() != DefaultReadTimeout {
		t.Errorf("unset timeout should be default, got %v", c.ReadTimeout())
	}
}
