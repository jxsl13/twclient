package client

import (
	"context"
	"testing"
)

// T28 rounds out the reconnect test matrix. Most cases live next to their
// feature:
//   - backoff sequence/cap/reset, policy ctor/options ...... backoff_test.go
//   - MaxAttempts bound, ctx-abort, Close-abort, gating ..... reconnect_auto_test.go
//   - reason classification + ban-duration parse ........... reconnect_test.go
//   - timeout code stable + send gating (vanilla degrade) .. timeout_test.go
//   - identity preserved across reconnect, ResetTimeoutCode . reconnect_identity_test.go
// The cases below cover the remaining gaps: clean disconnect on shutdown and
// idempotent Close (V40).

// V40: a deliberate Close sends a clean disconnect to the server (sess.Close,
// which emits CTRL_CLOSE) and marks the client closing so a concurrent drop is
// not auto-reconnected.
func TestCloseSendsCleanDisconnect(t *testing.T) {
	s := &stubSession{}
	c := New("localhost:8303")
	c.sess = s

	if err := c.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if s.closes != 1 {
		t.Errorf("session Close (CTRL_CLOSE) calls = %d, want 1", s.closes)
	}
	if !c.closing.Load() {
		t.Error("client should be marked closing after Close")
	}
	// After a deliberate Close, auto-reconnect must not start.
	c.connectCtx = context.Background()
	c.maybeAutoReconnect()
	// closed channel is shut; a reconnect loop would observe it immediately.
	select {
	case <-c.closed:
	default:
		t.Error("Close should have shut the closed channel")
	}
}

// V40: Close is idempotent — a second call neither panics nor re-closes a nil
// session.
func TestCloseIdempotent(t *testing.T) {
	s := &stubSession{}
	c := New("localhost:8303")
	c.sess = s

	_ = c.Close()
	_ = c.Close() // must not panic / double-close the channel

	if s.closes != 1 {
		t.Errorf("session Close calls = %d, want 1 (idempotent)", s.closes)
	}
}

// V36: the backoff is reset at the start of each reconnect cycle so a fresh drop
// always begins at the base delay (verified via the injected fake here).
func TestReconnectLoopResetsBackoff(t *testing.T) {
	fb := &fakeBackoff{}
	c := New("localhost")
	pol := NewReconnectPolicy(WithBackoff(fb), WithMaxAttempts(1))
	c.reconnectLoop(context.Background(), pol)
	if fb.resets.Load() != 1 {
		t.Errorf("backoff resets = %d, want 1 at cycle start", fb.resets.Load())
	}
}
