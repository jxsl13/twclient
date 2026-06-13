package client

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBackoff counts Next calls and returns a tiny fixed delay.
type fakeBackoff struct {
	nexts   atomic.Int32
	resets  atomic.Int32
	delayMs time.Duration
}

func (f *fakeBackoff) Next() time.Duration {
	f.nexts.Add(1)
	if f.delayMs == 0 {
		return time.Millisecond
	}
	return f.delayMs
}
func (f *fakeBackoff) Reset() { f.resets.Add(1) }

// V36: the loop retries up to MaxAttempts then gives up. "localhost" (no port)
// makes every Connect fail instantly, so this is fast and network-free.
func TestReconnectLoopMaxAttempts(t *testing.T) {
	fb := &fakeBackoff{}
	c := New("localhost")
	pol := NewReconnectPolicy(WithBackoff(fb), WithMaxAttempts(3))

	done := make(chan struct{})
	go func() { c.reconnectLoop(t.Context(), pol); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect loop did not stop")
	}
	if n := fb.nexts.Load(); n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
	if fb.resets.Load() != 1 {
		t.Errorf("backoff should be reset once at loop start, got %d", fb.resets.Load())
	}
}

// V39: a cancelled context aborts the loop promptly during the backoff wait.
func TestReconnectLoopCtxAbort(t *testing.T) {
	fb := &fakeBackoff{delayMs: time.Hour} // would block forever without ctx abort
	c := New("localhost")
	pol := NewReconnectPolicy(WithBackoff(fb))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { c.reconnectLoop(ctx, pol); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx cancel did not abort the reconnect loop")
	}
}

// V40: Close aborts the loop (and a deliberate close is not auto-reconnected).
func TestReconnectLoopCloseAbort(t *testing.T) {
	fb := &fakeBackoff{delayMs: time.Hour}
	c := New("localhost")
	pol := NewReconnectPolicy(WithBackoff(fb))

	done := make(chan struct{})
	go func() { c.reconnectLoop(t.Context(), pol); close(done) }()

	_ = c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not abort the reconnect loop")
	}
}

// V40: maybeAutoReconnect does nothing when disabled or when closing.
func TestMaybeAutoReconnectGating(t *testing.T) {
	// Disabled policy → no loop (would panic on nil ctx handling otherwise).
	c := New("localhost", WithoutAutoReconnect())
	c.connectCtx = t.Context()
	c.maybeAutoReconnect() // must be a no-op

	// Closing → no loop.
	c2 := New("localhost")
	c2.connectCtx = t.Context()
	c2.closing.Store(true)
	c2.maybeAutoReconnect()
}
