package client

import (
	"context"
	"testing"
	"time"
)

// V33: Reconnect preserves the client identity (name/clan/skin/country/
// password) and the stable timeout code even when the reconnect itself fails —
// none of these are reset by the reconnect path, so the next attempt resumes.
func TestReconnectPreservesIdentity(t *testing.T) {
	c := New("127.0.0.1:1",
		WithPlayerInfo("bob", "clanx", "santa", 5),
		WithPassword("pw"),
		WithTimeoutCode("code123"),
	)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	// The address is unreachable, so this fails — that's fine; we only assert
	// the identity survives the attempt.
	_ = c.Reconnect(ctx)

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

// V32/V37: ResetTimeoutCode regenerates the code (or sets a given one) so the
// next reconnect gets a fresh tee instead of resuming.
func TestResetTimeoutCode(t *testing.T) {
	c := New("127.0.0.1:1", WithTimeoutCode("original"))

	// No argument → new random non-empty code.
	c.ResetTimeoutCode()
	if c.TimeoutCode() == "original" || c.TimeoutCode() == "" {
		t.Errorf("ResetTimeoutCode() should produce a new non-empty code, got %q", c.TimeoutCode())
	}

	// Explicit code → set exactly.
	c.ResetTimeoutCode("chosen")
	if c.TimeoutCode() != "chosen" {
		t.Errorf("ResetTimeoutCode(\"chosen\"): got %q", c.TimeoutCode())
	}

	// Empty string → regenerate, not empty.
	c.ResetTimeoutCode("")
	if c.TimeoutCode() == "" || c.TimeoutCode() == "chosen" {
		t.Errorf("ResetTimeoutCode(\"\") should regenerate, got %q", c.TimeoutCode())
	}
}
