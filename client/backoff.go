package client

import "time"

// Backoff is a pluggable retry-delay schedule used by auto-reconnect (V36).
// Implementations are not required to be safe for concurrent use; the client
// drives a single backoff from one reconnect loop at a time.
type Backoff interface {
	// Next returns the delay to wait before the next attempt and advances the
	// internal state.
	Next() time.Duration
	// Reset returns the schedule to its initial delay (called after a
	// successful connect).
	Reset()
}

// ExponentialBackoff doubles the delay on each consecutive retry up to a cap.
// The default (DefaultBackoff) starts at 1s, doubles, and caps at 1h — the cap
// then acts as the steady-state poll interval between reconnect/unban tries.
// Fields are unexported so a zero/partial literal cannot bypass the validation
// in the constructor (V41); build it with NewExponentialBackoff.
type ExponentialBackoff struct {
	base   time.Duration
	max    time.Duration
	factor float64
	cur    time.Duration
}

// NewExponentialBackoff builds an exponential backoff. Invalid inputs fall back
// to sane defaults: base ≤ 0 → 1s, factor < 1 → 2, max < base → base.
func NewExponentialBackoff(base time.Duration, factor float64, max time.Duration) *ExponentialBackoff {
	if base <= 0 {
		base = time.Second
	}
	if factor < 1 {
		factor = 2
	}
	if max < base {
		max = base
	}
	return &ExponentialBackoff{base: base, max: max, factor: factor, cur: base}
}

// DefaultBackoff is NewExponentialBackoff(1s, 2, 1h).
func DefaultBackoff() Backoff {
	return NewExponentialBackoff(time.Second, 2, time.Hour)
}

// Next returns the current delay and advances toward the cap.
func (b *ExponentialBackoff) Next() time.Duration {
	d := b.cur
	next := time.Duration(float64(b.cur) * b.factor)
	if next > b.max || next <= 0 {
		next = b.max
	}
	b.cur = next
	return d
}

// Reset returns the delay to the base value.
func (b *ExponentialBackoff) Reset() { b.cur = b.base }

// ReconnectPolicy configures the client's automatic reconnection (T26). Build
// it with NewReconnectPolicy / DefaultReconnectPolicy (V41); fields are
// unexported so options are the only way to set them.
type ReconnectPolicy struct {
	enabled           bool
	maxAttempts       int // 0 = unlimited
	backoff           Backoff
	resumeWithTimeout bool
}

// ReconnectOption configures a ReconnectPolicy.
type ReconnectOption func(*ReconnectPolicy)

// DefaultReconnectPolicy is enabled, unlimited attempts, DefaultBackoff, and
// resumes the tee via the timeout code.
func DefaultReconnectPolicy() ReconnectPolicy {
	return ReconnectPolicy{
		enabled:           true,
		maxAttempts:       0,
		backoff:           DefaultBackoff(),
		resumeWithTimeout: true,
	}
}

// NewReconnectPolicy builds a policy from the defaults plus the given options.
func NewReconnectPolicy(opts ...ReconnectOption) ReconnectPolicy {
	p := DefaultReconnectPolicy()
	for _, opt := range opts {
		if opt != nil { // a nil option is ignored (V70)
			opt(&p)
		}
	}
	if p.backoff == nil {
		p.backoff = DefaultBackoff()
	}
	return p
}

// WithMaxAttempts caps the number of reconnect attempts (0 = unlimited).
func WithMaxAttempts(n int) ReconnectOption {
	return func(p *ReconnectPolicy) { p.maxAttempts = n }
}

// WithBackoff sets a custom backoff schedule.
func WithBackoff(b Backoff) ReconnectOption {
	return func(p *ReconnectPolicy) {
		if b != nil {
			p.backoff = b
		}
	}
}

// WithResumeTimeout controls whether reconnects resume the tee via the timeout
// code (true, default) or start fresh (false).
func WithResumeTimeout(resume bool) ReconnectOption {
	return func(p *ReconnectPolicy) { p.resumeWithTimeout = resume }
}
