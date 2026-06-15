//go:build e2e

package e2e

import (
	"context"
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
	// Both cases run against the SMALL-map DDNet sixup server (dm1, ~6 KB): the
	// default "Sunny Side Up" (~1.3 MB) would make the 0.7 serial-windowed vital
	// download take ~50s under this loss, without exercising any extra protocol
	// path — a tiny map drives the SAME loss machinery in seconds (see compose
	// ddnet-small). One sixup server serves BOTH 0.6 and 0.7.
	addr := env("TW_E2E_DDNET_SMALL", "ddnet-small:8303")
	cases := []struct {
		name    string
		version packet.Version
		pol     udpfault.Policy
		skip    string // non-empty → documented parity exception (not yet supported)
	}{
		// 0.6 tolerates heavy bidirectional loss (full vital retransmission).
		{"ddnet-0.6", packet.Version06, udpfault.Policy{DropC2S: 0.2, DropS2C: 0.2, Seed: 1}, ""},
		// 0.7 under FULL BIDIRECTIONAL loss, now resilient end-to-end (parity with
		// 0.6, V107). Every login phase retransmits — handshake CONNECT (B21a) plus,
		// crucially, a dropped CTRL_ACCEPT: the DDNet sixup server accepts straight
		// into an ONLINE slot and never re-sends ACCEPT (DirectInit + ClientExists,
		// network_server.cpp), so the client PRESUMES online after a few intervals
		// and sends INFO speculatively (T164/V128); INFO (recvUntilMapChange),
		// map-download MAP_DATA gaps (resend-flag, T162/T164) and READY / CON_READY
		// (fixed-cadence resend, T163) all recover under loss.
		{"ddnet-0.7", packet.Version07, udpfault.Policy{DropC2S: 0.2, DropS2C: 0.2, Seed: 1}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}
			px, err := udpfault.New(addr, tc.pol)
			if err != nil {
				t.Fatalf("proxy: %v", err)
			}
			t.Cleanup(func() { _ = px.Close() })

			// 20% bidirectional loss + retransmission needs far more than the 1s
			// login budget — even on the small map the 0.7 serial-windowed download
			// recovers each lost in-order vital over a round-trip, ~20s here and
			// more on a slow CI runner; 45s leaves headroom. auto-reconnect OFF so a
			// genuine login failure surfaces.
			c := client.New(px.Addr(), client.WithVersion(tc.version), client.WithoutAutoReconnect())
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			t.Cleanup(cancel)
			start := time.Now()
			if err := c.Connect(ctx); err != nil {
				t.Fatalf("%s: connect under loss failed: %v (dropped %d)", tc.name, err, px.Dropped())
			}
			t.Logf("%s: connect under loss took %s (dropped %d)", tc.name, time.Since(start).Round(time.Millisecond), px.Dropped())
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
