//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packet"
)

// This file brings the high-level client up against the docker-compose harness
// (see README.md). The TestE2E* scaffolds (T132) assert, per server, that login
// completes and a NON-EMPTY snapshot arrives (dbg_dummies → multi-character).
// TestE2EParity (T137) decodes the snapshots through the shared decoders and
// asserts 0.6 and 0.7 produce EQUIVALENT shared objects on the same server.

// requireHarness skips the whole suite unless TW_E2E=1 — it must never run as
// part of the ordinary `go test ./...` (it also needs the `e2e` build tag). When
// the harness IS enabled it also blocks (once) until the servers accept connects,
// so the live tests — which now hard-FAIL on a refused connect rather than skip —
// don't race container startup (`docker compose up -d` returns before the servers
// finish loading their maps).
func requireHarness(t *testing.T) {
	t.Helper()
	if os.Getenv("TW_E2E") != "1" {
		t.Skip("e2e harness disabled; set TW_E2E=1 after `docker compose -f e2e/docker-compose.yml up -d` (see e2e/README.md)")
	}
	harnessReadyOnce.Do(waitHarnessReady)
	if harnessReadyErr != nil {
		t.Fatalf("e2e harness not ready: %v", harnessReadyErr)
	}
}

var (
	harnessReadyOnce sync.Once
	harnessReadyErr  error
)

// waitHarnessReady polls a real connect to the primary ddnet server (the slowest
// to come up — it loads the ~1.3 MB "Sunny Side Up" map) until it succeeds or a
// generous deadline elapses. The sibling servers start alongside it, so a ready
// ddnet means the harness is warm. Pins 0.6 (no auto-detect probe) + no
// auto-reconnect for a clean one-shot attempt.
func waitHarnessReady() {
	addr := env("TW_E2E_DDNET_06", "ddnet:8303")
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		c := client.New(addr, client.WithVersion(packet.Version06), client.WithoutAutoReconnect())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := c.Connect(ctx)
		cancel()
		_ = c.Close()
		if err == nil {
			return
		}
		last = err
		time.Sleep(500 * time.Millisecond)
	}
	harnessReadyErr = fmt.Errorf("ddnet %s did not accept a connect within 60s: %w", addr, last)
}

// debugLogger returns a stderr debug logger when TW_DEBUG is set, else nil
// (Sessions accept a nil logger via the default).
func debugLogger() *slog.Logger {
	if os.Getenv("TW_DEBUG") == "" {
		return nil
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

const (
	// The harness is on localhost / the docker bridge — no network latency, so a
	// short timeout is plenty and makes the failure cases (unreachable, banned)
	// fail FAST instead of hanging. Login includes the (tiny, local) map download.
	loginTimeout = 1 * time.Second
	// Snapshots arrive every ~2 ticks (≈40ms) once in-game.
	snapTimeout = 1 * time.Second
)

// capture07 logs in over 0.7 (net7) and returns the first non-empty snapshot.
func capture07(t *testing.T, addr string) (*packet.Snapshot, error) {
	t.Helper()
	var opts []net7.Option
	if l := debugLogger(); l != nil {
		opts = append(opts, net7.WithLogger(l))
	}
	s, err := net7.NewSession(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), loginTimeout)
	defer cancel()
	if err := s.Login(ctx, "twe2e", ""); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	s.StartReader(ctx)
	defer s.StopReader()

	deadline := time.After(snapTimeout)
	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("no non-empty snapshot within %s", snapTimeout)
		case ev, ok := <-s.EventCh():
			if !ok {
				return nil, fmt.Errorf("event channel closed before a snapshot arrived")
			}
			if snap, isSnap := ev.(packet.EventSnapshot); isSnap && snap.Snap != nil && len(snap.Snap.Items) > 0 {
				return snap.Snap, nil
			}
		}
	}
}

// capture06 is the 0.6 (net6) counterpart of capture07.
func capture06(t *testing.T, addr string) (*packet.Snapshot, error) {
	t.Helper()
	var opts []net6.Option
	if l := debugLogger(); l != nil {
		opts = append(opts, net6.WithLogger(l))
	}
	s, err := net6.NewSession(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), loginTimeout)
	defer cancel()
	if err := s.Login(ctx, "twe2e", ""); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	s.StartReader(ctx)
	defer s.StopReader()

	deadline := time.After(snapTimeout)
	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("no non-empty snapshot within %s", snapTimeout)
		case ev, ok := <-s.EventCh():
			if !ok {
				return nil, fmt.Errorf("event channel closed before a snapshot arrived")
			}
			if snap, isSnap := ev.(packet.EventSnapshot); isSnap && snap.Snap != nil && len(snap.Snap.Items) > 0 {
				return snap.Snap, nil
			}
		}
	}
}

// reportSnapshot logs the captured object layout (typeID -> field count), the
// same ground-truth shape the live probes emit. T137 will turn this into a
// per-object parity assertion.
func reportSnapshot(t *testing.T, label, addr string, snap *packet.Snapshot) {
	t.Helper()
	sizes := map[int]int{}
	for _, it := range snap.Items {
		sizes[it.TypeID] = len(it.Fields)
	}
	t.Logf("%s snapshot from %s: tick=%d items=%d typeID->fieldCount=%v",
		label, addr, snap.Tick, len(snap.Items), sizes)
}

// NOTE: vanilla teeworlds 0.7 flood-bans repeated connects from one IP, so the
// suite makes EXACTLY ONE connection to the vanilla server — TestE2EVanilla07Entities
// (which captures + decodes + asserts) subsumes a separate login/snapshot probe.

func TestE2EDDNet07(t *testing.T) {
	requireHarness(t)
	addr := os.Getenv("TW_E2E_DDNET_07")
	if addr == "" {
		t.Skip("TW_E2E_DDNET_07 unset (DDNet sixup 0.7 address)")
	}
	snap, err := capture07(t, addr)
	if err != nil {
		t.Fatalf("ddnet sixup 0.7 %s: %v", addr, err)
	}
	reportSnapshot(t, "ddnet-0.7", addr, snap)
}

func TestE2EDDNet06(t *testing.T) {
	requireHarness(t)
	addr := os.Getenv("TW_E2E_DDNET_06")
	if addr == "" {
		t.Skip("TW_E2E_DDNET_06 unset (DDNet sixup 0.6 address)")
	}
	snap, err := capture06(t, addr)
	if err != nil {
		t.Fatalf("ddnet sixup 0.6 %s: %v", addr, err)
	}
	reportSnapshot(t, "ddnet-0.6", addr, snap)
}

// TestE2EParity is the T137 closer for V106/V107: the SAME DDNet sixup server
// (dbg_dummies) is captured over BOTH 0.6 and 0.7, each snapshot is run through
// its protocol decoder (net6/net7.DecodeSnap), and the resulting shared
// packet.SnapObjects are asserted EQUIVALENT in shape — the protocol-neutral
// repr (V112) carries the same kinds of objects regardless of wire version. The
// net6-only-score bug (B9/V106) is exactly what this would catch: if 0.7 decoded
// into empty Players/Characters while 0.6 did not, the parity check fails.
func TestE2EParity(t *testing.T) {
	requireHarness(t)
	addr6 := os.Getenv("TW_E2E_DDNET_06")
	addr7 := os.Getenv("TW_E2E_DDNET_07")
	if addr6 == "" || addr7 == "" {
		t.Skip("TW_E2E_DDNET_06 / TW_E2E_DDNET_07 unset (same DDNet sixup server, 0.6 + 0.7)")
	}

	snap6, err := capture06(t, addr6)
	if err != nil {
		t.Fatalf("ddnet 0.6 %s: %v", addr6, err)
	}
	snap7, err := capture07(t, addr7)
	if err != nil {
		t.Fatalf("ddnet 0.7 %s: %v", addr7, err)
	}

	o6 := net6.DecodeSnap(snap6)
	o7 := net7.DecodeSnap(snap7)
	t.Logf("0.6 decoded: chars=%d players=%d hasGameInfo=%t", len(o6.Characters), len(o6.Players), o6.HasGameInfo)
	t.Logf("0.7 decoded: chars=%d players=%d hasGameInfo=%t", len(o7.Characters), len(o7.Players), o7.HasGameInfo)

	// Characters: dbg_dummies (source-debug build, V116) guarantees SEVERAL on
	// BOTH protocols, so the snapshot is genuinely multi-character (> 1, not just
	// the client's own tee). The crux of parity — a protocol that decoded into
	// too few characters (wrong ids/fields) fails here while the other passes.
	if len(o6.Characters) <= 1 {
		t.Errorf("0.6 decoded %d characters, want > 1 (dbg_dummies bots, V116)", len(o6.Characters))
	}
	if len(o7.Characters) <= 1 {
		t.Errorf("0.7 decoded %d characters, want > 1 (dbg_dummies bots, V116)", len(o7.Characters))
	}

	// Scoreboard players present on BOTH (the net6-only-score bug, V106).
	if len(o6.Players) == 0 {
		t.Error("0.6 decoded zero players")
	}
	if len(o7.Players) == 0 {
		t.Error("0.7 decoded zero players")
	}

	// Game state object present on BOTH protocols.
	if !o6.HasGameInfo {
		t.Error("0.6 missing GameInfo")
	}
	if !o7.HasGameInfo {
		t.Error("0.7 missing GameInfo")
	}

	// A decoded character must carry a plausible (non-zero) position on each
	// protocol — proves the field layout (not just the count) decoded, on both.
	if !anyCharPositioned(o6.Characters) {
		t.Error("0.6 characters all at origin (field layout likely wrong)")
	}
	if !anyCharPositioned(o7.Characters) {
		t.Error("0.7 characters all at origin (field layout likely wrong)")
	}
}

// anyCharPositioned reports whether at least one character has a non-zero
// position (a sanity check that the per-field decode worked, not just the item
// count).
func anyCharPositioned(chars map[int]packet.Character) bool {
	for _, c := range chars {
		if c.X != 0 || c.Y != 0 {
			return true
		}
	}
	return false
}

// TestE2EVanilla07Entities decodes the vanilla teeworlds 0.7 CTF snapshot and
// asserts the shared repr carries the CTF entities DDRace lacks — flags and
// pickups — so the 0.7 decoder's flag/pickup paths (V112/V113) are exercised
// against a real server, complementing the DDNet character/player parity.
func TestE2EVanilla07Entities(t *testing.T) {
	requireHarness(t)
	addr := os.Getenv("TW_E2E_VANILLA_07")
	if addr == "" {
		t.Skip("TW_E2E_VANILLA_07 unset (vanilla teeworlds 0.7 CTF server)")
	}
	snap, err := capture07(t, addr)
	if err != nil {
		t.Fatalf("vanilla 0.7 %s: %v", addr, err)
	}
	o := net7.DecodeSnap(snap)
	t.Logf("vanilla-0.7 decoded: chars=%d flags=%d pickups=%d", len(o.Characters), len(o.Flags), len(o.Pickups))
	if len(o.Characters) <= 1 {
		t.Errorf("vanilla 0.7 decoded %d characters, want > 1 (dbg_dummies bots, V116)", len(o.Characters))
	}
	// A CTF map carries two flags; pickups (weapons/armor/health) are also
	// present. Require flags at minimum (the DDRace server cannot provide them).
	if len(o.Flags) == 0 {
		t.Error("vanilla 0.7 CTF map decoded zero flags")
	}
}
