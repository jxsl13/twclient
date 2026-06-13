package master_test

import (
	"testing"

	"github.com/jxsl13/twclient/master"
	"github.com/jxsl13/twclient/packet"
)

// V70: nil/empty options, a bad URL, and an unsupported version must not panic
// — they're ignored, clamped, or returned as errors.
func TestHostileInputNoPanic(t *testing.T) {
	c := master.New(nil, master.WithMasters(nil), master.WithQueryTimeout(-1)) // nil opt + empty masters + neg timeout

	if _, err := c.FetchServerListFrom(t.Context(), "http://nonexistent.invalid./x"); err == nil {
		t.Error("unreachable URL should return an error")
	}
	if _, err := c.QueryServerInfo(t.Context(), packet.Version(99), "127.0.0.1:1"); err == nil {
		t.Error("unsupported version should return an error")
	}
	if _, ok := master.ParseAddress(""); ok {
		t.Error("empty address should be rejected")
	}
}
