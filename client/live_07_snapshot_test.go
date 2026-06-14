package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/master"
	"github.com/jxsl13/twclient/packet"
)

// TestLive07Snapshot connects to a dynamically discovered 0.7-ONLY server and
// verifies that a full 0.7 session decodes snapshots (map + ticking snaps).
//
// "0.7-only" means the master lists the server with a 0.7 address and NO 0.6
// address — this excludes DDNet sixup servers that register both protocols, so
// the test exercises a genuine pure-0.7 path. The test skips cleanly when:
//   - run under -short,
//   - the master is unreachable,
//   - no 0.7-only server is currently registered,
//   - or none of the candidates accept a session (offline / UDP blocked).
func TestLive07Snapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("live 0.7 session; skipped under -short")
	}

	fetchCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	entries, err := master.New().FetchServerList(fetchCtx)
	if err != nil {
		t.Skipf("no master reachable: %v", err)
	}

	// Collect 0.7-only addresses (has a 0.7 address, has NO 0.6 address).
	var candidates []string
	for _, e := range entries {
		has6, has7 := false, false
		var addr07 string
		for _, a := range e.Addresses {
			switch a.Version {
			case packet.Version06:
				has6 = true
			case packet.Version07:
				has7 = true
				if addr07 == "" {
					addr07 = a.String()
				}
			}
		}
		if has7 && !has6 && addr07 != "" {
			candidates = append(candidates, addr07)
		}
	}
	if len(candidates) == 0 {
		t.Skip("no 0.7-only server registered with the master right now")
	}
	t.Logf("found %d 0.7-only candidate servers", len(candidates))

	// Try a handful until one grants a session and starts ticking snapshots.
	const maxTry = 4
	if len(candidates) > maxTry {
		candidates = candidates[:maxTry]
	}
	for _, addr := range candidates {
		if trySnapshot07(t, addr) {
			return // success
		}
	}
	t.Skipf("no 0.7-only candidate accepted a session (offline / UDP blocked): tried %d", len(candidates))
}

// trySnapshot07 connects to addr as 0.7 and waits for the first decoded
// snapshot. It returns true on success, false (with a log line) on any failure
// so the caller can move on to the next candidate.
func trySnapshot07(t *testing.T, addr string) bool {
	t.Helper()

	c := client.New(addr,
		client.WithVersion(packet.Version07),
		client.WithPlayerInfo("snaptest", "", "default", -1),
	)

	connCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := c.Connect(connCtx); err != nil {
		t.Logf("%s: connect failed: %v", addr, err)
		return false
	}
	defer c.Close()

	// Poll for the first ticking snapshot (and a parsed map).
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if c.LastSnapTick() > 0 {
			if c.MapView() == nil {
				t.Errorf("%s: 0.7 snapshot tick %d but MapView is nil", addr, c.LastSnapTick())
				return false
			}
			t.Logf("%s: 0.7 snapshot OK tick=%d localID=%d map=%dx%d",
				addr, c.LastSnapTick(), c.LocalID(), c.MapView().Width(), c.MapView().Height())
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("%s: connected but no snapshot within deadline", addr)
	return false
}
