//go:build e2e

package e2e

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/packet"
)

// Flood-safe reconnect regression tests (T187/V141/V142) against UNPATCHED,
// flood-protection-ON servers (teeworlds7-flood: "Stressing network" ban intact;
// ddnet-flood: DEFAULT sv_connlimit 5/20s) — distinct from the patched/raised
// fast suite (V140). They prove the LIBRARY's flood-safe reconnect policy
// (DefaultBackoff 3s base + ≥60s cooldown on flood/ban CLOSE, T186) does NOT
// trip a real server's connect-flood ban across repeated drop→reconnect cycles.
//
// Ban-trigger enumeration (T185, from teeworlds 0.7 + DDNet network_server.cpp):
//
//	#     server      trigger                              threshold              response                        persistent
//	TW-1  tw 0.7      conn ERRORS <1s after connect        1s (CNetServer::Update) BanAddr 60s "Stressing network"  YES (60s IP ban)
//	TW-2  tw 0.7      max clients same IP (concurrent)     sv_max_clients_per_ip   CLOSE "Only N..."                no (concurrency)
//	DD-1  ddnet       connect rate                         sv_connlimit 5 / 20s    CLOSE "Too many connections..."  no (window)
//	DD-2  ddnet       max clients same IP                  sv_max_clients_per_ip   CLOSE "Only N..."                no (concurrency)
//	DD-3  ddnet       antispoof conn/s                     sv_van_conn_per_second  dm1 map fallback (NOT a ban)     no
//
// Reconnect-policy-relevant: TW-1 (the dangerous persistent ban) + DD-1. A
// retry-storm of fast-failing connects feeds TW-1; >5 reconnects/20s feeds DD-1.
// The 3s flood-safe backoff keeps reconnects ≤3/20s and never immediate, so
// neither trips. Concurrency (TW-2/DD-2) is not a reconnect-rate concern.

func floodTW7() string      { return env("TW_E2E_TW7_FLOOD", "teeworlds7-flood:8303") }
func floodTW7Econ() string  { return env("TW_E2E_TW7_FLOOD_ECON", "teeworlds7-flood:9303") }
func floodDDNet() string    { return env("TW_E2E_DDNET_FLOOD", "ddnet-flood:8303") }
func floodDDNetEcon() string { return env("TW_E2E_DDNET_FLOOD_ECON", "ddnet-flood:9303") }

// V141/V142: with the DEFAULT (flood-safe) reconnect policy, repeated econ-kick
// → auto-reconnect cycles against a flood-protection-ON server all re-establish
// and NONE is refused as a connect-flood/ban (DisconnectKindFlooded/Banned) —
// the 3s backoff keeps the connect rate under TW-1 + DD-1. Under the old 1s base
// these cycles would crowd the window and (on teeworlds) trip the 60s ban.
func TestFloodSafeReconnect(t *testing.T) {
	requireHarness(t)
	// ONE protocol per flood IP: subtests against the SAME server IP run
	// back-to-back, and DDNet's connlimit counts per IP across them — two
	// 3-cycle subtests on one ddnet-flood IP would SUM past 5/20s and self-flood
	// (not a policy fault). ddnet-flood (0.6) exercises DD-1; teeworlds7-flood
	// (0.7) exercises the dangerous TW-1 persistent ban. Both triggers covered.
	servers := []struct {
		name    string
		version packet.Version
		addr    string
		econ    string
	}{
		{"ddnet-flood-0.6", packet.Version06, floodDDNet(), floodDDNetEcon()},   // DD-1
		{"teeworlds7-flood", packet.Version07, floodTW7(), floodTW7Econ()},      // TW-1
	}
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			if s.addr == "" {
				t.Skip("addr unset")
			}
			// DEFAULT policy: auto-reconnect ON, flood-safe DefaultBackoff (T186).
			c := client.New(s.addr, client.WithVersion(s.version))
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			t.Cleanup(cancel)
			if err := c.Connect(ctx); err != nil {
				t.Fatalf("%s: initial connect refused: %v", s.name, err)
			}
			t.Cleanup(func() { _ = c.Close() })

			var floodBan atomic.Bool
			c.OnDisconnect(func(_ *client.Client, r client.DisconnectReason) {
				if r.Kind == client.DisconnectKindFlooded || r.Kind == client.DisconnectKindBanned {
					floodBan.Store(true)
				}
			})
			waitSnapshot(t, c)

			conn := dialEcon(t, s.econ)
			t.Cleanup(func() { _ = conn.WriteLine("unban_all") })

			// Drive several drop→auto-reconnect cycles. The flood-safe backoff
			// paces them so the server's flood limiter never fires.
			const cycles = 3
			for i := range cycles {
				id := waitLocalID(t, c)
				if id < 0 {
					t.Fatalf("%s: local id never appeared (cycle %d)", s.name, i)
				}
				if err := conn.WriteLine("kick " + strconv.Itoa(id) + " flood-test"); err != nil {
					t.Fatalf("%s: econ kick (cycle %d): %v", s.name, i, err)
				}
				// Wait for the auto-reconnect to re-establish in-game.
				deadline := time.Now().Add(30 * time.Second)
				reconnected := false
				lastTick := c.LastSnapTick()
				for time.Now().Before(deadline) {
					if c.IsConnected() && c.LastSnapTick() > lastTick {
						reconnected = true
						break
					}
					if floodBan.Load() {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				if floodBan.Load() {
					t.Fatalf("%s: flood/ban CLOSE during reconnect (cycle %d) — policy tripped the server flood limit (V141)", s.name, i)
				}
				if !reconnected {
					t.Fatalf("%s: did not auto-reconnect within 30s (cycle %d)", s.name, i)
				}
				t.Logf("%s: cycle %d reconnected (tick=%d)", s.name, i, c.LastSnapTick())
			}
			if floodBan.Load() {
				t.Errorf("%s: observed a flood/ban CLOSE — flood-safe policy regressed (V141)", s.name)
			}
		})
	}
}
