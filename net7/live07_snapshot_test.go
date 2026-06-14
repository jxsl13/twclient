package net7_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jxsl13/twclient/master"
	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packet"
)

// find07OnlyServers returns live PURE-0.7 server addresses: entries that
// advertise a 0.7 address and NO 0.6 address, so they are native teeworlds 0.7
// servers, not DDNet sixup servers (which serve both 0.6 and 0.7). It skips the
// test when the master list is unreachable or no such server exists (T128).
// Set TW_07_ADDR to pin a specific server and bypass discovery.
func find07OnlyServers(t *testing.T) []string {
	t.Helper()
	if addr := os.Getenv("TW_07_ADDR"); addr != "" {
		return []string{addr}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entries, err := master.New().FetchServerList(ctx)
	if err != nil {
		t.Skipf("master list unavailable: %v", err)
	}
	var addrs []string
	for _, e := range entries {
		var has07, has06 bool
		var a07 master.Address
		for _, a := range e.Addresses {
			switch a.Version {
			case packet.Version07:
				has07, a07 = true, a
			case packet.Version06:
				has06 = true
			}
		}
		if has07 && !has06 {
			addrs = append(addrs, a07.String())
		}
	}
	if len(addrs) == 0 {
		t.Skip("no pure-0.7 (non-sixup) server found in master list")
	}
	return addrs
}

// captureSnapshot connects to one 0.7 server and returns its first non-empty
// snapshot, or false if the connect/login/recv did not complete in time (live
// servers are unreliable — full, restarting, firewalled).
func captureSnapshot(t *testing.T, addr string) (*packet.Snapshot, bool) {
	t.Helper()
	var opts []net7.Option
	if os.Getenv("TW_DEBUG") != "" {
		opts = append(opts, net7.WithLogger(slog.New(slog.NewTextHandler(os.Stderr,
			&slog.HandlerOptions{Level: slog.LevelDebug}))))
	}
	s, err := net7.NewSession(addr, opts...)
	if err != nil {
		return nil, false
	}
	defer s.Close()

	// Login now downloads the map before READY (T131); DDNet maps can be a few
	// MB, so allow generous time.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := s.Login(ctx, "tw07probe", ""); err != nil {
		t.Logf("skip %s: login: %v", addr, err)
		return nil, false
	}
	s.StartReader(ctx)
	defer s.StopReader()

	deadline := time.After(6 * time.Second)
	for {
		select {
		case <-deadline:
			return nil, false
		case ev, ok := <-s.EventCh():
			if !ok {
				return nil, false
			}
			if snap, isSnap := ev.(packet.EventSnapshot); isSnap && snap.Snap != nil && len(snap.Snap.Items) > 0 {
				return snap.Snap, true
			}
		}
	}
}

// TestLive07Snapshot captures a REAL 0.7 snapshot from a dynamically discovered
// 0.7-only server and reports its object type-id → field-count distribution —
// the ground truth needed to build the net7 known-size table and validate 0.7
// snapshot decoding (T121/T128, V108). It skips when no 0.7-only server is
// reachable; it is a live-network test, skipped under -short.
func TestLive07Snapshot(t *testing.T) {
	if os.Getenv("TW_LIVE") == "" {
		t.Skip("live-network test; set TW_LIVE=1 to run (V118)")
	}
	addrs := find07OnlyServers(t)
	t.Logf("found %d pure-0.7 server(s)", len(addrs))

	// Try several servers — any single one may be full / restarting / firewalled.
	const maxTry = 6
	for i, addr := range addrs {
		if i >= maxTry {
			break
		}
		snap, ok := captureSnapshot(t, addr)
		if !ok {
			continue
		}
		sizes := map[int]int{}
		for _, it := range snap.Items {
			sizes[it.TypeID] = len(it.Fields)
		}
		t.Logf("real 0.7 snapshot from %s: tick=%d items=%d typeID→fieldCount=%v",
			addr, snap.Tick, len(snap.Items), sizes)
		return
	}
	t.Skipf("no snapshot captured from %d 0.7-only server(s) tried", min(len(addrs), maxTry))
}

// find07SixupServers returns the 0.7 address of servers that ALSO speak 0.6 —
// i.e. DDNet sixup servers (the common, reliable 0.7 deployment). Skips if none.
func find07SixupServers(t *testing.T) []string {
	t.Helper()
	if addr := os.Getenv("TW_07_ADDR"); addr != "" {
		return []string{addr}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entries, err := master.New().FetchServerList(ctx)
	if err != nil {
		t.Skipf("master list unavailable: %v", err)
	}
	var addrs []string
	for _, e := range entries {
		var has07, has06 bool
		var a07 master.Address
		for _, a := range e.Addresses {
			switch a.Version {
			case packet.Version07:
				has07, a07 = true, a
			case packet.Version06:
				has06 = true
			}
		}
		if has07 && has06 {
			addrs = append(addrs, a07.String())
		}
	}
	if len(addrs) == 0 {
		t.Skip("no DDNet sixup (0.6+0.7) server found in master list")
	}
	return addrs
}

// TestLive07SnapshotSixup is a FIRST end-to-end 0.7 validation against DDNet
// sixup servers (0.6+0.7) — more available than vanilla-only 0.7. It exercises
// the full login (incl. the pre-READY map download, T131) + snapshot decode and
// logs the captured object layout. Skips if none reachable; -short skips it.
func TestLive07SnapshotSixup(t *testing.T) {
	if os.Getenv("TW_LIVE") == "" {
		t.Skip("live-network test; set TW_LIVE=1 to run (V118)")
	}
	addrs := find07SixupServers(t)
	t.Logf("found %d DDNet sixup 0.7 server(s)", len(addrs))

	const maxTry = 3
	for i, addr := range addrs {
		if i >= maxTry {
			break
		}
		snap, ok := captureSnapshot(t, addr)
		if !ok {
			continue
		}
		sizes := map[int]int{}
		for _, it := range snap.Items {
			sizes[it.TypeID] = len(it.Fields)
		}
		t.Logf("0.7 sixup snapshot from %s: tick=%d items=%d typeID→fieldCount=%v",
			addr, snap.Tick, len(snap.Items), sizes)
		return
	}
	t.Skipf("no snapshot captured from %d sixup server(s) tried", min(len(addrs), maxTry))
}
