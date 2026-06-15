package client

import (
	"context"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// lifetimeStub embeds stubSession and captures the context StartReader receives,
// plus optionally blocks Login on its ctx (to test the connect-sequence bound).
type lifetimeStub struct {
	*stubSession
	readerCtx  context.Context
	blockLogin bool
}

func (s *lifetimeStub) StartReader(ctx context.Context) { s.readerCtx = ctx }

func (s *lifetimeStub) Login(ctx context.Context, _ string, _ string, _ ...packet.LoginOption) error {
	if s.blockLogin {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// Regression (issue #4, B25): the ctx passed to Connect must bound only the
// connect sequence, NOT the session. Cancelling it after Connect must NOT stop
// the live reader/session — only Close does.
func TestConnectCtxDoesNotKillSession(t *testing.T) {
	stub := &lifetimeStub{stubSession: &stubSession{}}
	c := New("x:8303")
	c.newSessionFn = func() (Session, error) { return stub, nil }

	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if stub.readerCtx == nil {
		t.Fatal("StartReader was not called")
	}

	// Cancel the caller's Connect ctx — the session must survive.
	cancel()
	time.Sleep(30 * time.Millisecond)
	select {
	case <-stub.readerCtx.Done():
		t.Fatal("reader ctx cancelled by the caller's Connect ctx — session would die (#4)")
	default:
	}
	if !c.IsConnected() {
		t.Error("IsConnected() = false after caller ctx cancel; session was killed")
	}

	// Close DOES end the session.
	_ = c.Close()
	select {
	case <-stub.readerCtx.Done():
	case <-time.After(2 * time.Second):
		t.Error("Close did not cancel the reader ctx")
	}
}

// WithConnectTimeout bounds the connect sequence: a Login that blocks on its ctx
// fails with the deadline, without needing a deadline'd caller ctx (V136).
func TestWithConnectTimeoutBoundsHandshake(t *testing.T) {
	stub := &lifetimeStub{stubSession: &stubSession{}, blockLogin: true}
	c := New("x:8303", WithConnectTimeout(50*time.Millisecond))
	c.newSessionFn = func() (Session, error) { return stub, nil }

	start := time.Now()
	err := c.Connect(context.Background()) // long-lived ctx; the timeout still bounds
	if err == nil {
		t.Fatal("Connect should fail when Login exceeds WithConnectTimeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Connect took %s; WithConnectTimeout(50ms) did not bound the handshake", elapsed)
	}
}
