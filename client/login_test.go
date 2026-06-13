package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// V42 + login defaults: a fresh client uses the DDNet/Teeworlds defaults, and
// WithPassword/WithPlayerInfo override them.
func TestClientLoginDefaults(t *testing.T) {
	c := New("localhost:8303")
	if c.name != packet.DefaultName {
		t.Errorf("default name: want %q, got %q", packet.DefaultName, c.name)
	}
	if c.skin != packet.DefaultSkin {
		t.Errorf("default skin: want %q, got %q", packet.DefaultSkin, c.skin)
	}
	if c.country != packet.DefaultCountry {
		t.Errorf("default country: want %d, got %d", packet.DefaultCountry, c.country)
	}
	if c.password != "" {
		t.Errorf("default password: want empty, got %q", c.password)
	}
}

func TestWithPassword(t *testing.T) {
	c := New("localhost:8303", WithPassword("letmein"))
	if c.password != "letmein" {
		t.Errorf("WithPassword: want %q, got %q", "letmein", c.password)
	}
}
