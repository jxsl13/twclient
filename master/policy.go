package master

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
)

// RequestPolicy decides how Client.FetchServerList selects among the configured
// masters. The interface drives the whole strategy (not just an order), so a
// policy may probe concurrently and/or cache state across calls (V64).
type RequestPolicy interface {
	// Fetch performs one FetchServerList against masters using try (fetch +
	// validate a single master URL). It returns the first valid list, or an
	// error if every master fails. Implementations may be stateful.
	Fetch(ctx context.Context, masters []string, try func(context.Context, string) ([]ServerEntry, error)) ([]ServerEntry, error)
}

// errNoMasters is returned when the master list is empty.
var errNoMasters = fmt.Errorf("master: no masters configured")

// wrapAllFailed wraps the last error from a fully-failed attempt.
func wrapAllFailed(last error) error {
	if last == nil {
		return errNoMasters
	}
	return fmt.Errorf("master: all masters failed: %w", last)
}

// Failover tries masters in order [0..n) and returns the first success (V64).
func Failover() RequestPolicy { return failover{} }

type failover struct{}

// Fetch tries masters in order and returns the first success (RequestPolicy).
func (failover) Fetch(ctx context.Context, masters []string, try func(context.Context, string) ([]ServerEntry, error)) ([]ServerEntry, error) {
	var last error
	for _, m := range masters {
		entries, err := try(ctx, m)
		if err == nil {
			return entries, nil
		}
		last = err
	}
	return nil, wrapAllFailed(last)
}

// RoundRobin rotates the starting master each call (shared atomic cursor →
// spreads load across calls), then failover through the rest (V64).
func RoundRobin() RequestPolicy { return &roundRobin{} }

type roundRobin struct{ cursor atomic.Uint64 }

// Fetch rotates the start master each call, then failover through the rest
// (RequestPolicy).
func (r *roundRobin) Fetch(ctx context.Context, masters []string, try func(context.Context, string) ([]ServerEntry, error)) ([]ServerEntry, error) {
	n := len(masters)
	if n == 0 {
		return nil, errNoMasters
	}
	start := int((r.cursor.Add(1) - 1) % uint64(n))
	var last error
	for i := 0; i < n; i++ {
		entries, err := try(ctx, masters[(start+i)%n])
		if err == nil {
			return entries, nil
		}
		last = err
	}
	return nil, wrapAllFailed(last)
}

// ChooseFastest replicates DDNet's CChooseMaster (§R): probe the masters
// concurrently in random order, the first valid (no-error) response wins
// (fastest healthy), cache that master, reuse it on later calls, and re-probe
// on its failure. Default policy.
func ChooseFastest() RequestPolicy { return &chooseFastest{best: -1} }

type chooseFastest struct {
	mu   sync.Mutex
	best int // cached winning master index, -1 = none yet
}

// Fetch reuses the cached best master, else concurrently probes all in random
// order and caches the fastest valid one (RequestPolicy; DDNet CChooseMaster).
func (c *chooseFastest) Fetch(ctx context.Context, masters []string, try func(context.Context, string) ([]ServerEntry, error)) ([]ServerEntry, error) {
	n := len(masters)
	if n == 0 {
		return nil, errNoMasters
	}

	// Fast path: reuse the cached best master if it still works.
	c.mu.Lock()
	best := c.best
	c.mu.Unlock()
	if best >= 0 && best < n {
		if entries, err := try(ctx, masters[best]); err == nil {
			return entries, nil
		}
		// cached master failed → fall through to a fresh probe
	}

	// Probe all masters concurrently in random order; first success wins and is
	// cached, then the rest are cancelled.
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		idx     int
		entries []ServerEntry
		err     error
	}
	ch := make(chan result, n)
	for _, idx := range rand.Perm(n) {
		go func(idx int) {
			entries, err := try(probeCtx, masters[idx])
			ch <- result{idx, entries, err}
		}(idx)
	}
	var last error
	for range n {
		r := <-ch
		if r.err == nil {
			c.mu.Lock()
			c.best = r.idx
			c.mu.Unlock()
			return r.entries, nil
		}
		last = r.err
	}
	return nil, wrapAllFailed(last)
}
