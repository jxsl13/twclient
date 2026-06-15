//go:build e2e

package e2e

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/internal/livegate"
	"github.com/jxsl13/twclient/packet"
)

// Live-server integration tests (SPEC V119). Table-driven over BOTH the DDNet
// sixup server (0.6 + 0.7) and the vanilla teeworlds 0.7 server, driving the
// FULL client through the dockerized harness and provoking error states
// out-of-band via econ. DDNet-only features (capabilities) skip on vanilla with
// a documented reason. All gated by requireHarness (TW_E2E + -tags e2e, V118).

// T150: full client.Connect → map download → decoded snapshot, on every server.
func TestLiveLoginSnapshot(t *testing.T) {
	requireHarness(t)
	for _, s := range liveServers() {
		t.Run(s.name, func(t *testing.T) {
			if s.addr == "" {
				t.Skip("addr unset")
			}
			c := dialClientOrSkip(t, s.version, s.addr)
			waitSnapshot(t, c)
			if c.LastSnapTick() <= 0 {
				t.Fatalf("%s: no snapshot tick", s.name)
			}
			t.Logf("%s: connected, snapshot tick=%d", s.name, c.LastSnapTick())
		})
	}
}

// T151: actions reach a live server without error and keep the session alive.
func TestLiveActions(t *testing.T) {
	requireHarness(t)
	for _, s := range liveServers() {
		t.Run(s.name, func(t *testing.T) {
			if s.addr == "" {
				t.Skip("addr unset")
			}
			c := dialClientOrSkip(t, s.version, s.addr)
			waitSnapshot(t, c)
			acts := []client.Action{
				client.ActChat{Msg: "tw-e2e hello"},
				client.ActEmoticon{Emoticon: packet.Emoticon(0)},
				client.ActSetTeam{Team: 0},
				client.ActSetSpectator{TargetID: -1},
				client.ActKill{},
			}
			for _, a := range acts {
				if err := c.Do(a); err != nil {
					t.Errorf("%s: Do(%T): %v", s.name, a, err)
				}
			}
			// The session must survive the action burst (still receiving snaps).
			tick := c.LastSnapTick()
			time.Sleep(200 * time.Millisecond)
			if !c.IsConnected() || c.LastSnapTick() < tick {
				t.Errorf("%s: session not healthy after actions (connected=%t tick %d→%d)",
					s.name, c.IsConnected(), tick, c.LastSnapTick())
			}
		})
	}
}

// T152: client-side rcon — login, run a command, observe an rcon line. Both
// servers set sv_rcon_password (T149); skip any that lacks rcon (documented).
func TestLiveRcon(t *testing.T) {
	requireHarness(t)
	for _, s := range liveServers() {
		t.Run(s.name, func(t *testing.T) {
			if s.addr == "" {
				t.Skip("addr unset")
			}
			var lines atomic.Int32
			c := dialClientOrSkip(t, s.version, s.addr, client.WithRconPassword(rconPassword))
			c.OnRconLine(func(_ *client.Client, _ packet.EventRconLine) { lines.Add(1) })

			// WithRconPassword auto-logs-in after connect; wait for auth.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && !c.RconAuthed() {
				time.Sleep(20 * time.Millisecond)
			}
			if !c.RconAuthed() {
				t.Fatalf("%s: rcon not authenticated", s.name)
			}
			if err := c.Rcon("echo tw-e2e-rcon"); err != nil {
				t.Fatalf("%s: Rcon: %v", s.name, err)
			}
			deadline = time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && lines.Load() == 0 {
				time.Sleep(20 * time.Millisecond)
			}
			if lines.Load() == 0 {
				t.Errorf("%s: no rcon line received after echo", s.name)
			}
		})
	}
}

// T154: wrong-password connect → real CTRL_CLOSE → WrongPassword, both servers.
func TestLiveWrongPassword(t *testing.T) {
	requireHarness(t)
	cases := []struct {
		name    string
		version packet.Version
		addr    string
	}{
		{"ddnet-pw", packet.Version06, ddnetPw()},
		{"teeworlds7-pw", packet.Version07, tw7Pw()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := tryConnect(t, tc.version, tc.addr, client.WithPassword("definitely-wrong"))
			if err == nil {
				t.Fatalf("%s: connect with wrong password SUCCEEDED, want rejection", tc.name)
			}
			if reason := c.LastDisconnect(); reason.Kind != client.DisconnectKindWrongPassword {
				t.Errorf("%s: disconnect kind=%s, want wrong_password (err=%v)", tc.name, reason.Kind, err)
			}
		})
	}
}

// T153: live auto-reconnect (both servers) + DDNet capabilities (DDNet-only).
func TestLiveReconnect(t *testing.T) {
	requireHarness(t)
	// Connect-heavy (each test reconnects) → use the dedicated vanilla instance
	// to avoid the shared-server flood-ban (V120).
	for _, s := range liveServersHeavy() {
		if s.econ == "" {
			continue
		}
		t.Run("reconnect/"+s.name, func(t *testing.T) {
			if s.addr == "" {
				t.Skip("addr unset")
			}
			// Auto-reconnect ON (default). connectCtx must outlive the drop, so a
			// generous timeout (⊥ dialClient's 1s login ctx).
			c := client.New(s.addr, client.WithVersion(s.version))
			release := livegate.Enter(s.version) // serialize+space 0.7 connects (B17/V120), before the ctx timer
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			t.Cleanup(cancel)
			err := c.Connect(ctx)
			release()
			if err != nil {
				t.Skipf("%s: connect refused (harness state, not a code defect): %v", s.name, err)
			}
			t.Cleanup(func() { _ = c.Close() })
			waitSnapshot(t, c)

			var drops atomic.Int32
			c.OnDisconnect(func(_ *client.Client, _ client.DisconnectReason) { drops.Add(1) })

			id := waitLocalID(t, c)
			if id < 0 {
				t.Skip("local id never appeared")
			}
			conn := dialEcon(t, s.econ)
			t.Cleanup(func() { _ = conn.WriteLine("unban_all") })
			if err := conn.WriteLine("kick " + strconv.Itoa(id) + " tw-e2e-reconnect"); err != nil {
				t.Fatalf("%s: econ kick: %v", s.name, err)
			}
			// Observe the drop, then the auto-reconnect re-establishing in-game.
			deadlineDrop := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadlineDrop) && drops.Load() == 0 {
				time.Sleep(20 * time.Millisecond)
			}
			if drops.Load() == 0 {
				t.Fatalf("%s: no disconnect observed after kick", s.name)
			}
			// Auto-reconnect: back in-game with a fresh snapshot.
			deadlineUp := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadlineUp) {
				if c.IsConnected() && c.LastSnapTick() > 0 {
					t.Logf("%s: auto-reconnected after kick (tick=%d)", s.name, c.LastSnapTick())
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatalf("%s: did not auto-reconnect within 20s", s.name)
		})
	}
}

// T153: server capabilities — DDNet announces them; vanilla 0.7 does not (the
// documented DDNet-only feature, V119/V47/V107).
func TestLiveCapabilities(t *testing.T) {
	requireHarness(t)
	for _, s := range liveServers() {
		t.Run(s.name, func(t *testing.T) {
			if s.addr == "" {
				t.Skip("addr unset")
			}
			c := dialClientOrSkip(t, s.version, s.addr)
			waitSnapshot(t, c)
			caps := c.Capabilities()
			if s.isDDNet {
				// DDNet announces NETMSG_CAPABILITIES to BOTH 0.6 and 0.7/sixup
				// clients; net6 + net7 parse it (V124/T157).
				if !caps.DDNet {
					t.Errorf("%s: DDNet server did not report DDNet capabilities (%+v)", s.name, caps)
				}
				t.Logf("%s: DDNet caps ok (DDNet=%t chatTimeout=%t)", s.name, caps.DDNet, caps.ChatTimeoutCode)
			} else {
				// Vanilla teeworlds 0.7 sends no DDNet caps (V119 exception).
				if caps.DDNet {
					t.Errorf("%s: vanilla server unexpectedly announced DDNet caps", s.name)
				}
				t.Logf("%s: no DDNet caps (vanilla, as expected)", s.name)
			}
		})
	}
}

// T156: live error-state matrix. econ-provoked drops (kick) + transport errors
// (unreachable, ctx). Both servers where the trigger exists (V119).
func TestLiveErrorStates(t *testing.T) {
	requireHarness(t)

	t.Run("unreachable", func(t *testing.T) {
		// A dead port on a real host → connect fails fast, ⊥ hang.
		_, err := tryConnect(t, packet.Version06, "ddnet:65000")
		if err == nil {
			t.Fatal("connect to dead port succeeded, want error")
		}
	})

	t.Run("ctx-cancel", func(t *testing.T) {
		// A cancelled context aborts Connect promptly (V39), ⊥ hang.
		c := client.New(env("TW_E2E_DDNET_06", "ddnet:8303"),
			client.WithVersion(packet.Version06), client.WithoutAutoReconnect())
		t.Cleanup(func() { _ = c.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // pre-cancelled
		if err := c.Connect(ctx); err == nil {
			t.Fatal("connect with cancelled context succeeded, want error")
		}
	})

	// kick connects per server → vanilla uses the dedicated instance (V120).
	for _, s := range liveServersHeavy() {
		if s.econ == "" {
			continue
		}
		t.Run("kick/"+s.name, func(t *testing.T) {
			if s.addr == "" {
				t.Skip("addr unset")
			}
			// Clear any residual ban on the shared test IP first — the ban subtest
			// above bans this IP and its unban may not have propagated before this
			// connect, which would otherwise refuse the client (server sent CLOSE).
			conn := dialEcon(t, s.econ)
			_ = conn.WriteLine("unban_all")
			time.Sleep(200 * time.Millisecond)
			c := dialClientOrSkip(t, s.version, s.addr)
			waitSnapshot(t, c)
			id := waitLocalID(t, c)
			if id < 0 {
				t.Skipf("%s: local client id never appeared (cannot target kick)", s.name)
			}
			// Kick exactly the test client OUT-OF-BAND via econ.
			if err := conn.WriteLine("kick " + strconv.Itoa(id) + " tw-e2e"); err != nil {
				t.Fatalf("%s: econ kick: %v", s.name, err)
			}
			reason, dropped := waitDisconnected(t, c, 3*time.Second)
			if !dropped {
				t.Fatalf("%s: client not dropped after econ kick", s.name)
			}
			// The real CTRL_CLOSE classifies as Kicked (or at least a close).
			if reason.Kind != client.DisconnectKindKicked && reason.Kind != client.DisconnectKindClosed {
				t.Errorf("%s: kick reason kind=%s, want kicked/closed", s.name, reason.Kind)
			}
			t.Logf("%s: econ-kicked client %d → %s", s.name, id, reason.Kind)
		})
	}

	// ban runs LAST: it bans the shared test IP for 5 min, which would refuse the
	// sibling kick connects above — running it last keeps them on a clean IP. Its
	// own defensive unban (at start + on cleanup) clears a stale ban a prior run's
	// ban subtest may have left (a banned IP can't connect to register the unban).
	// DDNet only — one connect, avoids the vanilla flood-ban (B17/V120).
	t.Run("ban/ddnet-0.6", func(t *testing.T) {
		addr := env("TW_E2E_DDNET_06", "ddnet:8303")
		conn := dialEcon(t, ddnetEcon())
		_ = conn.WriteLine("unban_all")
		t.Cleanup(func() { _ = conn.WriteLine("unban_all") })
		time.Sleep(200 * time.Millisecond) // let the unban take effect before dialing
		c := dialClientOrSkip(t, packet.Version06, addr)
		waitSnapshot(t, c)
		id := waitLocalID(t, c)
		if id < 0 {
			t.Skip("local id never appeared")
		}
		// ban <id> <minutes> <reason>
		if err := conn.WriteLine("ban " + strconv.Itoa(id) + " 5 tw-e2e"); err != nil {
			t.Fatalf("econ ban: %v", err)
		}
		reason, dropped := waitDisconnected(t, c, 3*time.Second)
		if !dropped {
			t.Fatal("client not dropped after econ ban")
		}
		if reason.Kind != client.DisconnectKindBanned && reason.Kind != client.DisconnectKindClosed {
			t.Errorf("ban reason kind=%s, want banned/closed", reason.Kind)
		}
		t.Logf("econ-banned client %d → %s (dur=%s)", id, reason.Kind, reason.BanDuration)
	})
}

// T178: the player registry MUST populate from the live snapshot on 0.6 at
// PARITY with 0.7 against the SAME DDNet sixup server — ids at minimum, names
// when the server carries them (issue #6, B26/V137). The 0.6 join/score/team is
// DERIVED from the snapshot (deriveRoster06 / deriveGame), not delivered as a
// reader message, and was previously dispatched only to callbacks, never the
// registry, so Roster() stayed empty while 0.7 (reader Sv_ClientInfo) populated.
// This is the LIVE regression V137 demands — the synthetic snapshot unit test
// missed it twice (#3, #6); only a real dbg_dummies connect catches the gap.
func TestLiveRosterPopulated(t *testing.T) {
	requireHarness(t)
	for _, s := range liveServers() {
		t.Run(s.name, func(t *testing.T) {
			if s.addr == "" {
				t.Skip("addr unset")
			}
			c := dialClientOrSkip(t, s.version, s.addr)
			waitSnapshot(t, c)

			// Registry fills from the post-ENTERGAME messages + first few
			// snapshots, not snapshot #1 (V121/B18) — poll up to the warm-up.
			var roster []client.PlayerState
			named := 0
			deadline := time.Now().Add(registryWarmup)
			for time.Now().Before(deadline) {
				roster = c.Roster()
				named = 0
				for _, p := range roster {
					if p.Name != "" {
						named++
					}
				}
				if len(roster) > 0 && named > 0 {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}

			t.Logf("%s: roster=%d named=%d localID=%d", s.name, len(roster), named, c.LocalID())
			// dbg_dummies bots → several entries on BOTH protocols (V116/V107).
			if len(roster) == 0 {
				t.Fatalf("%s: Roster() empty after %s warm-up (issue #6/V137 — registry not populated from snapshot)", s.name, registryWarmup)
			}
			if named == 0 {
				t.Errorf("%s: Roster() has %d entries but none named (identity not populated)", s.name, len(roster))
			}
		})
	}
}

// T181: protocol AUTO-DETECT (V138/V139). With NO WithVersion, Connect probes
// the server connlessly and picks the protocol — the DDNet sixup server speaks
// BOTH, so detection must resolve it to 0.6 (preference); the vanilla server is
// 0.7-only, so it resolves to 0.7. Each detected client then reaches a snapshot.
// Direct server probe, no master list (V138) — these harness servers have none.
func TestLiveAutoDetect(t *testing.T) {
	requireHarness(t)
	cases := []struct {
		name string
		addr string
		want packet.Version
	}{
		{"ddnet (both → prefer 0.6)", env("TW_E2E_DDNET_06", "ddnet:8303"), packet.Version06},
		{"vanilla-0.7 (only 0.7)", env("TW_E2E_VANILLA_07", "teeworlds7:8303"), packet.Version07},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.addr == "" {
				t.Skip("addr unset")
			}
			// No WithVersion → default auto-detect. Generous connect ctx: the
			// detect probe runs before handshake/login/map-download.
			c := client.New(tc.addr, client.WithoutAutoReconnect())
			// Gate by the EXPECTED version (the 0.7 target's connless probe is what
			// the vanilla flood-ban would drop, B17/V120), before the ctx timer.
			release := livegate.Enter(tc.want)
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			t.Cleanup(cancel)
			err := c.Connect(ctx)
			release()
			if err != nil {
				t.Skipf("connect %s refused (harness state, not a code defect): %v", tc.addr, err)
			}
			t.Cleanup(func() { _ = c.Close() })

			if got := c.Version(); got != tc.want {
				t.Errorf("%s: auto-detected version = %v, want %v", tc.name, got, tc.want)
			}
			waitSnapshot(t, c)
			t.Logf("%s: auto-detected %v, snapshot tick=%d", tc.name, c.Version(), c.LastSnapTick())
		})
	}
}
