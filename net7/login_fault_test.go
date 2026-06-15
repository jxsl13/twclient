package net7

import (
	"context"
	"testing"
	"time"

	"github.com/jxsl13/twclient/internal/udpfault"
)

// TestLoginThroughProxy drives the 0.7 login handshake through the seeded UDP
// fault proxy (V125): a clean mock server, with loss/latency injected BOTH
// directions between client and server. Login must still complete via vital
// retransmission (B6/V68) — exercising the resend paths the bespoke
// drop-first-N mock cannot reach by direction.
func TestLoginThroughProxy(t *testing.T) {
	// Client→server (request) loss + latency: the client resends the pending
	// vital and the (stateless-enough) mock replies on the resend. Server→client
	// (response) loss recovery is exercised against the LIVE server via the proxy
	// (TestE2ELoginUnderLoss) — the simple mock advances on first reply and can't
	// re-reply to a resend whose response was lost (a mock limitation, not a
	// client one: the Handshake/recv loops do resend, V68/B6).
	cases := []struct {
		name string
		pol  udpfault.Policy
	}{
		{"no-loss", udpfault.Policy{}},
		{"drop-c2s-30pct", udpfault.Policy{DropC2S: 0.3, Seed: 2}},
		{"drop-c2s-50pct", udpfault.Policy{DropC2S: 0.5, Seed: 5}},
		{"latency", udpfault.Policy{LatencyC2S: 25 * time.Millisecond, LatencyS2C: 25 * time.Millisecond, Seed: 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := lossyMockServer(t, 0) // clean server; the proxy injects the faults
			px, err := udpfault.New(addr, tc.pol)
			if err != nil {
				t.Fatalf("proxy: %v", err)
			}
			t.Cleanup(func() { _ = px.Close() })

			s, err := NewSession(px.Addr())
			if err != nil {
				t.Fatalf("session: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })

			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			t.Cleanup(cancel)
			if err := s.Login(ctx, "twfault", ""); err != nil {
				t.Fatalf("login [%s] did not complete under faults: %v (dropped %d)", tc.name, err, px.Dropped())
			}
		})
	}
}
