//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/internal/udpfault"
	"github.com/jxsl13/twclient/packet"
)

// TestE2ELoginUnderLoss drives the FULL client through the seeded UDP fault
// proxy in front of the LIVE DDNet server, with loss injected in BOTH directions
// (V125). Unlike the mock (which can't re-reply to a resend whose response was
// lost), a real server handles retransmits, so this exercises the COMPLETE
// retransmission path — incl. server→client response loss — plus snapshot decode
// under ongoing loss. Both protocols (V107 parity).
func TestE2ELoginUnderLoss(t *testing.T) {
	requireHarness(t)
	cases := []struct {
		name    string
		version packet.Version
		env     string
		pol     udpfault.Policy
		skip    string // non-empty → documented parity exception (not yet supported)
	}{
		// 0.6 tolerates heavy bidirectional loss (full vital retransmission).
		{"ddnet-0.6", packet.Version06, "TW_E2E_DDNET_06", udpfault.Policy{DropC2S: 0.2, DropS2C: 0.2, Seed: 1}, ""},
		// 0.7: PARTIAL. The map-download is now loss-resilient (T162: request-resend
		// + in-order vital reassembly — verified to drain under c2s loss). But the
		// 0.7 login still has TWO deeper loss gaps that form a chain: (a) a dropped
		// CTRL_ACCEPT deadlocks (server won't re-ACCEPT a duplicate CONNECT), and
		// (b) the READY→CON_READY wait doesn't reliably resend READY when other
		// traffic resets its timer. Full 0.7 loss-resilience is a larger reliability
		// effort (B21/T162-open). 0.6 covers full bidirectional loss today.
		{"ddnet-0.7", packet.Version07, "TW_E2E_DDNET_07", udpfault.Policy{DropC2S: 0.3, Seed: 1}, "0.7 login con_ready/accept not yet loss-resilient (T163/T164)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}
			addr := os.Getenv(tc.env)
			if addr == "" {
				t.Skipf("%s unset", tc.env)
			}
			px, err := udpfault.New(addr, tc.pol)
			if err != nil {
				t.Fatalf("proxy: %v", err)
			}
			t.Cleanup(func() { _ = px.Close() })

			// 20% bidirectional loss + retransmission needs more than the 1s login
			// budget; auto-reconnect OFF so a genuine login failure surfaces.
			c := client.New(px.Addr(), client.WithVersion(tc.version), client.WithoutAutoReconnect())
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			t.Cleanup(cancel)
			if err := c.Connect(ctx); err != nil {
				t.Fatalf("%s: connect under loss failed: %v (dropped %d)", tc.name, err, px.Dropped())
			}
			t.Cleanup(func() { _ = c.Close() })

			// A snapshot must still decode under ongoing loss (delta/ack recover).
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if c.LastSnapTick() > 0 {
					t.Logf("%s: login + snapshot tick=%d under loss (dropped %d)", tc.name, c.LastSnapTick(), px.Dropped())
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatalf("%s: no snapshot within 15s under loss (dropped %d)", tc.name, px.Dropped())
		})
	}
}
