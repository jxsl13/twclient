package client

import (
	"testing"
	"time"
)

// V141(a): the DEFAULT reconnect backoff keeps connect attempts under DDNet's
// sv_connlimit (5 per 20s) with margin, and the first retry is not immediate
// (which would feed vanilla teeworlds' "errored within 1s" → 60s ban).
func TestDefaultBackoffFloodSafe(t *testing.T) {
	b := DefaultBackoff()
	var cum time.Duration
	connects := 0
	for range 10 {
		cum += b.Next()
		if cum <= 20*time.Second {
			connects++
		}
	}
	if connects > 3 {
		t.Errorf("DefaultBackoff yields %d connects within 20s, want ≤3 (flood-safe vs ddnet 5/20s, V141)", connects)
	}
	if first := DefaultBackoff().Next(); first < time.Second {
		t.Errorf("first reconnect delay %s < 1s — risks vanilla errored-within-1s ban (V141)", first)
	}
}

// V141(c): connect-flood / ban CLOSE reasons classify so the reconnect loop can
// apply the cooldown; ordinary drops stay their own kind.
func TestDisconnectKindFlooded(t *testing.T) {
	cases := map[string]DisconnectKind{
		"Too many connections in a short time": DisconnectKindFlooded, // DDNet sv_connlimit
		"Stressing network":                    DisconnectKindFlooded, // vanilla tw rate ban
		"You have been banned for 5 minutes":   DisconnectKindBanned,
		"Kicked (spam)":                         DisconnectKindKicked,
		"This server is full":                   DisconnectKindFull,
		"Wrong password":                        DisconnectKindWrongPassword,
	}
	for raw, want := range cases {
		if got := NewDisconnectReason(raw).Kind; got != want {
			t.Errorf("classify %q = %v, want %v", raw, got, want)
		}
	}
}

// V141(c): flood/ban/full refusals get the cooldown floor (or the longer
// server-stated ban duration); ordinary drops reconnect promptly (0 cooldown).
func TestReconnectCooldown(t *testing.T) {
	for _, k := range []DisconnectKind{DisconnectKindFlooded, DisconnectKindBanned, DisconnectKindFull} {
		if cd := reconnectCooldown(DisconnectReason{Kind: k}); cd < floodCooldown {
			t.Errorf("cooldown for %v = %s, want ≥%s", k, cd, floodCooldown)
		}
	}
	for _, k := range []DisconnectKind{DisconnectKindKicked, DisconnectKindClosed, DisconnectKindTimedOut, DisconnectKindShuttingDown} {
		if cd := reconnectCooldown(DisconnectReason{Kind: k}); cd != 0 {
			t.Errorf("cooldown for %v = %s, want 0 (reconnect promptly)", k, cd)
		}
	}
	if cd := reconnectCooldown(DisconnectReason{Kind: DisconnectKindBanned, BanDuration: 5 * time.Minute}); cd != 5*time.Minute {
		t.Errorf("banned-5m cooldown = %s, want 5m (stated duration > floor)", cd)
	}
}
