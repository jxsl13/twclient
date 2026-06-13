package client

import (
	"context"
	"testing"
	"time"
)

// V33: ReconnectWithTimeout preserves the client identity (name/clan/skin/
// country/password) and the stable timeout code even when the reconnect itself
// fails — none of these are reset by the reconnect path.
func TestReconnectWithTimeoutPreservesIdentity(t *testing.T) {
	c := New("127.0.0.1:1",
		WithPlayerInfo("bob", "clanx", "santa", 5),
		WithPassword("pw"),
		WithTimeoutCode("code123"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// The address is unreachable, so this fails — that's fine; we only assert
	// the identity survives the attempt.
	_ = c.ReconnectWithTimeout(ctx)

	if c.TimeoutCode() != "code123" {
		t.Errorf("timeout code changed: %q", c.TimeoutCode())
	}
	if c.name != "bob" || c.clan != "clanx" || c.skin != "santa" || c.country != 5 {
		t.Errorf("player info changed: name=%q clan=%q skin=%q country=%d", c.name, c.clan, c.skin, c.country)
	}
	if c.password != "pw" {
		t.Errorf("password changed: %q", c.password)
	}
}
