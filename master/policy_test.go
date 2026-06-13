package master

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// newTry builds a `try` closure plus a per-master hit counter map. Counters are
// pre-seeded for every master so the concurrent ChooseFastest probe only does
// atomic Add (no map writes → race-safe). resp maps a master to a forced error.
func newTry(masters []string, resp map[string]error) (func(context.Context, string) ([]ServerEntry, error), map[string]*atomic.Int32) {
	hits := make(map[string]*atomic.Int32, len(masters))
	for _, m := range masters {
		hits[m] = &atomic.Int32{}
	}
	try := func(_ context.Context, url string) ([]ServerEntry, error) {
		if h := hits[url]; h != nil {
			h.Add(1)
		}
		if err := resp[url]; err != nil {
			return nil, err
		}
		return []ServerEntry{{Location: url}}, nil // non-empty marker
	}
	return try, hits
}

var errDown = errors.New("down")

// V64: Failover tries in order, returns first success.
func TestFailoverPolicy(t *testing.T) {
	masters := []string{"a", "b", "c"}
	try, hits := newTry(masters, map[string]error{"a": errDown})
	got, err := Failover().Fetch(t.Context(), masters, try)
	if err != nil || len(got) != 1 || got[0].Location != "b" {
		t.Fatalf("got %+v err=%v, want b", got, err)
	}
	if h := hits["c"]; h != nil && h.Load() != 0 {
		t.Error("failover should stop at first success (c untouched)")
	}
}

// V64: RoundRobin advances the start master each call (load spread).
func TestRoundRobinPolicy(t *testing.T) {
	masters := []string{"a", "b", "c"}
	p := RoundRobin()
	try, hits := newTry(masters, nil) // all succeed
	for range 3 {
		if _, err := p.Fetch(t.Context(), masters, try); err != nil {
			t.Fatal(err)
		}
	}
	// 3 calls, rotating start → each master hit exactly once.
	for _, m := range masters {
		if hits[m].Load() != 1 {
			t.Errorf("master %q hit %d times, want 1 (round-robin rotation)", m, hits[m].Load())
		}
	}
}

// V64: RoundRobin still failover within a call when a master is down.
func TestRoundRobinFailover(t *testing.T) {
	masters := []string{"a", "b"}
	try, _ := newTry(masters, map[string]error{"a": errDown})
	got, err := RoundRobin().Fetch(t.Context(), masters, try)
	if err != nil || len(got) == 0 {
		t.Fatalf("want recovery via b, got err=%v", err)
	}
}

// V67: one Fetch exhausts every master exactly once (in rotation) before
// returning an error when all are down.
func TestRoundRobinExhaustsAllThenErrors(t *testing.T) {
	masters := []string{"a", "b", "c", "d"}
	allDown := map[string]error{"a": errDown, "b": errDown, "c": errDown, "d": errDown}
	try, hits := newTry(masters, allDown)

	_, err := RoundRobin().Fetch(t.Context(), masters, try)
	if err == nil {
		t.Fatal("want error when all masters are down")
	}
	for _, m := range masters {
		if got := hits[m].Load(); got != 1 {
			t.Errorf("master %q attempted %d times, want exactly 1 (each tried once before erroring)", m, got)
		}
	}
}

// V67: when only the rotation-start master is down, the call advances to the
// next master in rotation and recovers.
func TestRoundRobinStartDownRecovers(t *testing.T) {
	masters := []string{"a", "b", "c"}
	p := RoundRobin()
	// First call rotates start to "a" (cursor 0). Take "a" down → must advance
	// to "b" and succeed within the same call.
	try, hits := newTry(masters, map[string]error{"a": errDown})
	got, err := p.Fetch(t.Context(), masters, try)
	if err != nil || len(got) == 0 {
		t.Fatalf("want recovery via the next master, got err=%v", err)
	}
	if got[0].Location != "b" {
		t.Errorf("recovered via %q, want b (next in rotation after down start)", got[0].Location)
	}
	if hits["a"].Load() != 1 || hits["b"].Load() != 1 {
		t.Errorf("expected a then b attempted once each: a=%d b=%d", hits["a"].Load(), hits["b"].Load())
	}
	if hits["c"].Load() != 0 {
		t.Errorf("c should be untouched (b already succeeded): c=%d", hits["c"].Load())
	}
}

// V67: the rotation start advances across calls (load spread), so over n calls
// each master is the starting master once.
func TestRoundRobinRotatesStartAcrossCalls(t *testing.T) {
	masters := []string{"a", "b", "c"}
	p := RoundRobin()
	starts := make([]string, 0, 3)
	for range 3 {
		try, hits := newTry(masters, nil) // all up → only the start master is hit
		got, err := p.Fetch(t.Context(), masters, try)
		if err != nil {
			t.Fatal(err)
		}
		starts = append(starts, got[0].Location)
		hitCount := 0
		for _, m := range masters {
			hitCount += int(hits[m].Load())
		}
		if hitCount != 1 {
			t.Errorf("all up → exactly one master hit per call, got %d", hitCount)
		}
	}
	// Three consecutive calls → three distinct start masters (full rotation).
	seen := map[string]bool{starts[0]: true, starts[1]: true, starts[2]: true}
	if len(seen) != 3 {
		t.Errorf("start should rotate across 3 calls, got %v", starts)
	}
}

// V64: ChooseFastest picks a working master, caches it, and reuses it (the
// concurrent probe runs only on the first call).
func TestChooseFastestCachesBest(t *testing.T) {
	masters := []string{"a", "b", "c"}
	p := ChooseFastest()
	try, hits := newTry(masters, nil) // all valid

	first, err := p.Fetch(t.Context(), masters, try)
	if err != nil || len(first) == 0 {
		t.Fatalf("first fetch failed: %v", err)
	}
	total := func() int32 {
		var s int32
		for _, m := range masters {
			s += hits[m].Load()
		}
		return s
	}
	// The probe spawns one goroutine per master; Fetch returns on the first
	// success and cancels the rest, but those goroutines still call try once.
	// Wait until all 3 have landed before measuring (deadline-bounded).
	deadline := time.Now().Add(2 * time.Second)
	for total() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	afterProbe := total()
	if afterProbe != 3 {
		t.Fatalf("first call should probe all 3 masters, hit %d", afterProbe)
	}

	// Second call reuses the cached best → exactly one more hit total.
	if _, err := p.Fetch(t.Context(), masters, try); err != nil {
		t.Fatal(err)
	}
	if total() != afterProbe+1 {
		t.Errorf("second call should reuse cached best (1 hit), total %d→%d", afterProbe, total())
	}
}

// V64: ChooseFastest re-probes when the cached master goes down.
func TestChooseFastestReprobeOnFailure(t *testing.T) {
	masters := []string{"a", "b"}
	// down-state via atomics — ChooseFastest may still call try from a cancelled
	// probe goroutine after Fetch returns, so the closure must be race-safe; the
	// map is read-only (only the atomics it points to flip).
	down := map[string]*atomic.Bool{"a": {}, "b": {}}
	try := func(_ context.Context, url string) ([]ServerEntry, error) {
		if down[url].Load() {
			return nil, errDown
		}
		return []ServerEntry{{Location: url}}, nil
	}
	p := ChooseFastest()
	got, _ := p.Fetch(t.Context(), masters, try)
	cached := got[0].Location

	// Take the cached master down → next call must re-probe and find the other.
	down[cached].Store(true)
	got2, err := p.Fetch(t.Context(), masters, try)
	if err != nil || len(got2) == 0 || got2[0].Location == cached {
		t.Fatalf("re-probe should find a different live master, got %+v err=%v", got2, err)
	}
}

// V64: all masters down → error (every policy).
func TestPoliciesAllDown(t *testing.T) {
	masters := []string{"a", "b"}
	allDown := map[string]error{"a": errDown, "b": errDown}
	for name, p := range map[string]RequestPolicy{"failover": Failover(), "roundrobin": RoundRobin(), "fastest": ChooseFastest()} {
		try, _ := newTry(masters, allDown)
		if _, err := p.Fetch(t.Context(), masters, try); err == nil {
			t.Errorf("%s: want error when all masters down", name)
		}
	}
}

// V64: empty master list → error.
func TestPoliciesNoMasters(t *testing.T) {
	try, _ := newTry(nil, nil)
	for _, p := range []RequestPolicy{Failover(), RoundRobin(), ChooseFastest()} {
		if _, err := p.Fetch(t.Context(), nil, try); err == nil {
			t.Error("want error with no masters")
		}
	}
}
