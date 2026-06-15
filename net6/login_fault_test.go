package net6

import (
	"context"
	"testing"
	"time"

	"github.com/jxsl13/twclient/internal/udpfault"
)

// TestLoginThroughProxy drives the 0.6 login handshake through the seeded UDP
// fault proxy (V125): a clean mock server with client→server loss / latency
// injected between client and server. Login still completes via vital
// retransmission (B6/V68). Server→client response-loss recovery is covered
// against the live server (the simple mock can't re-reply to a resend, see net7).
func TestLoginThroughProxy(t *testing.T) {
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
			addr := lossyMockServer(t, 0)
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
