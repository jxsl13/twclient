package master

import (
	"context"
	"fmt"
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
