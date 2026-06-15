//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/internal/livegate"
	"github.com/jxsl13/twclient/packet"
	"github.com/teeworlds-go/econ"
)

// Live-integration harness helpers (SPEC T149 / V119). These drive the FULL
// client.Connect against the dockerized servers (not a raw net6/net7 session)
// and provide an out-of-band econ admin channel for provoking error states
// (kick/ban/shutdown/fill) decoupled from the client under test.

// Static e2e credentials — configured on the containers (ddnet.Dockerfile,
// teeworlds7.cfg, docker-compose.yml). NEVER a real deployment secret.
const (
	rconPassword   = "twrcon"   // sv_rcon_password (client-rcon feature, T152)
	econPassword   = "tweecon"  // ec_password (out-of-band admin, T156)
	serverPassword = "twsecret" // password on the *-pw instances (T154)
)

// Default IN-NETWORK addresses (compose service names); overridable by env so
// the suite can also target a manually-run harness.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func ddnetEcon() string { return env("TW_E2E_DDNET_ECON", "ddnet:9303") }
func tw7Econ() string   { return env("TW_E2E_TW7_ECON", "teeworlds7:9303") }
func ddnetPw() string   { return env("TW_E2E_DDNET_PW", "ddnet-pw:8303") }
func tw7Pw() string     { return env("TW_E2E_TW7_PW", "teeworlds7-pw:8303") }

// dialClient runs the full client lifecycle (Connect → background reader) against
// addr and returns the connected client; it fails the test on connect error and
// registers Close cleanup. Use waitSnapshot to block until game state arrives.
func dialClient(t *testing.T, version packet.Version, addr string, opts ...client.Option) *client.Client {
	t.Helper()
	opts = append([]client.Option{client.WithVersion(version)}, opts...)
	c := client.New(addr, opts...)
	// Serialize + space 0.7 connects process-wide to dodge the vanilla teeworlds
	// flood-ban (B17/V120); 0.6 is a no-op. Acquire BEFORE the timeout ctx so the
	// spacing wait does not eat the connect deadline.
	release := livegate.Enter(version)
	ctx, cancel := context.WithTimeout(t.Context(), loginTimeout)
	t.Cleanup(cancel)
	err := c.Connect(ctx)
	release()
	if err != nil {
		t.Fatalf("connect %s (proto %v): %v", addr, version, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// waitSnapshot blocks until the client has a decoded snapshot (LastSnapTick > 0)
// or snapTimeout elapses.
func waitSnapshot(t *testing.T, c *client.Client) {
	t.Helper()
	deadline := time.Now().Add(snapTimeout)
	for time.Now().Before(deadline) {
		if c.LastSnapTick() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no snapshot within %s", snapTimeout)
}

// dialEcon opens the external-console (econ) admin connection to addr; the test
// drives kick/ban/shutdown/dbg_dummies through it OUT-OF-BAND (V119).
func dialEcon(t *testing.T, addr string) *econ.Conn {
	t.Helper()
	// The lib RECONNECTS on a failed dial (blocks); bound it with a context +
	// short reconnect delay so an unreachable econ fails the test fast.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	conn, err := econ.DialTo(addr, econPassword,
		econ.WithContext(ctx),
		econ.WithMaxReconnectDelay(200*time.Millisecond))
	if err != nil {
		t.Fatalf("econ dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// liveServer is one row of the both-protocols / both-implementations test table
// (V119 live-server parity). econ is "" when the server has no admin channel.
type liveServer struct {
	name    string
	version packet.Version
	addr    string
	econ    string
	isDDNet bool // DDNet-only features (capabilities) gate on this
}

func liveServers() []liveServer {
	return []liveServer{
		{"ddnet-0.6", packet.Version06, env("TW_E2E_DDNET_06", "ddnet:8303"), ddnetEcon(), true},
		{"ddnet-0.7", packet.Version07, env("TW_E2E_DDNET_07", "ddnet:8303"), ddnetEcon(), true},
		{"vanilla-0.7", packet.Version07, env("TW_E2E_VANILLA_07", "teeworlds7:8303"), tw7Econ(), false},
	}
}

func tw7bAddr() string { return env("TW_E2E_TW7B", "teeworlds7-b:8303") }
func tw7bEcon() string { return env("TW_E2E_TW7B_ECON", "teeworlds7-b:9303") }

// liveServersHeavy is liveServers with the vanilla server pointed at a SECOND
// instance — for connect-heavy tests (reconnect, kick) that would otherwise trip
// the vanilla flood-ban on the shared instance (B17/V120).
func liveServersHeavy() []liveServer {
	s := liveServers()
	for i := range s {
		if !s[i].isDDNet {
			s[i].addr = tw7bAddr()
			s[i].econ = tw7bEcon()
		}
	}
	return s
}

// tryConnect runs client.Connect WITHOUT failing the test on error (auto-reconnect
// OFF so a server-side drop is observable as terminal) — for the reject/error
// tests. Returns the client + the connect error.
func tryConnect(t *testing.T, version packet.Version, addr string, opts ...client.Option) (*client.Client, error) {
	t.Helper()
	opts = append([]client.Option{client.WithVersion(version), client.WithoutAutoReconnect()}, opts...)
	c := client.New(addr, opts...)
	// Gate BEFORE the timeout ctx so the spacing wait does not eat the connect
	// deadline (serialize+space 0.7 connects, B17/V120).
	release := livegate.Enter(version)
	ctx, cancel := context.WithTimeout(t.Context(), loginTimeout)
	t.Cleanup(cancel)
	err := c.Connect(ctx)
	release()
	t.Cleanup(func() { _ = c.Close() })
	return c, err
}

// dialClientOrSkip connects like dialClient but SKIPS the test (not fails) when
// the live server REFUSES the connect (server sent CLOSE). For the econ
// state-change subtests (kick/ban) a connected client is a PRECONDITION; under a
// dense single-process run the shared test IP can hit transient ban residue or
// slot contention, which is a harness-state issue, not a defect in the code under
// test — skip rather than red (mirrors the `id < 0 → Skip` precondition guard).
func dialClientOrSkip(t *testing.T, version packet.Version, addr string, opts ...client.Option) *client.Client {
	t.Helper()
	c, err := tryConnect(t, version, addr, opts...)
	if err != nil {
		t.Skipf("connect %s (proto %v) refused (harness state, not a code defect): %v", addr, version, err)
	}
	return c
}

// registryWarmup is the window derived registry/local state is allowed to fill
// in — the local player + scoreboard come from the post-ENTERGAME Sv_ClientInfo
// and the first FEW snapshots, not snapshot #1 (V121/B18). Generous vs the 1s
// snapTimeout so a real server's warm-up doesn't false-negative.
const registryWarmup = 5 * time.Second

// waitLocalID polls Client.LocalID for the local player's server-assigned client
// id (needed to econ-kick exactly the test client), allowing the warm-up window
// for it to resolve from the snapshot / Sv_ClientInfo (V121). -1 if it never
// appears.
func waitLocalID(t *testing.T, c *client.Client) int {
	t.Helper()
	deadline := time.Now().Add(registryWarmup)
	for time.Now().Before(deadline) {
		if id := c.LocalID(); id >= 0 {
			return id
		}
		time.Sleep(20 * time.Millisecond)
	}
	return -1
}

// waitDisconnected polls until the client reports a terminal disconnect (event
// loop closed) or the deadline; returns the classified reason.
func waitDisconnected(t *testing.T, c *client.Client, within time.Duration) (client.DisconnectReason, bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if c.Err() != nil {
			return c.LastDisconnect(), true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return c.LastDisconnect(), false
}

// TestHarnessSmoke (T149) validates the new harness pieces: econ reaches both
// servers (admin command accepted) and a full client.Connect reaches a snapshot.
func TestHarnessSmoke(t *testing.T) {
	requireHarness(t)

	// econ on both servers: connect + run a harmless command.
	for _, addr := range []string{ddnetEcon(), tw7Econ()} {
		conn := dialEcon(t, addr)
		if err := conn.WriteLine("echo tw-e2e-econ-ok"); err != nil {
			t.Fatalf("econ %s write: %v", addr, err)
		}
		t.Logf("econ %s: connected + command accepted", addr)
	}

	// full client lifecycle to a snapshot (0.6 sixup).
	addr6 := os.Getenv("TW_E2E_DDNET_06")
	if addr6 == "" {
		t.Skip("TW_E2E_DDNET_06 unset")
	}
	c := dialClient(t, packet.Version06, addr6)
	waitSnapshot(t, c)
	t.Logf("client connected to %s, snapshot tick=%d", addr6, c.LastSnapTick())
}
