package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// V44: Rcon requires a session and authentication.
func TestRconRequiresAuth(t *testing.T) {
	c := New("localhost:8303")
	if err := c.Rcon("status"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("no session: want ErrNotConnected, got %v", err)
	}

	s := &stubSession{}
	c.sess = s
	if err := c.Rcon("status"); !errors.Is(err, ErrNotAuthed) {
		t.Errorf("not authed: want ErrNotAuthed, got %v", err)
	}
	if s.lastCall == "rconCmd" {
		t.Error("must not send rcon command before auth")
	}
}

// V44: auth state tracks EventRconAuth, and an authed client can send commands.
func TestRconAuthStateFromEvent(t *testing.T) {
	c := New("localhost:8303")
	s := &stubSession{}
	c.sess = s

	c.handleEvent(packet.EventRconAuth{Authed: true, Level: 1})
	if !c.RconAuthed() {
		t.Fatal("RconAuthed should be true after auth-on event")
	}
	if err := c.Rcon("shutdown"); err != nil {
		t.Fatalf("Rcon after auth: %v", err)
	}
	if s.rconCmd != "shutdown" {
		t.Errorf("rcon cmd = %q, want shutdown", s.rconCmd)
	}

	c.handleEvent(packet.EventRconAuth{Authed: false})
	if c.RconAuthed() {
		t.Error("RconAuthed should be false after auth-off event")
	}
}

func TestWithRconPassword(t *testing.T) {
	c := New("localhost:8303", WithRconPassword("secret"))
	if c.rconPassword != "secret" {
		t.Errorf("WithRconPassword: got %q", c.rconPassword)
	}
}

// RconLogin sends the auth request and returns once the server confirms.
func TestRconLoginAwaitsAuth(t *testing.T) {
	c := New("localhost:8303")
	s := &stubSession{}
	c.sess = s

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.RconLogin(ctx, "pw") }()

	// Let the auth request go out, then simulate the server confirmation.
	time.Sleep(30 * time.Millisecond)
	c.handleEvent(packet.EventRconAuth{Authed: true})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RconLogin: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RconLogin did not return after auth confirmation")
	}
	// Safe to read the stub now that RconLogin has returned.
	if s.rconAuthPw != "pw" {
		t.Errorf("auth password sent = %q, want pw", s.rconAuthPw)
	}
}

// RconLogin honors the context deadline when the server never confirms.
func TestRconLoginTimeout(t *testing.T) {
	c := New("localhost:8303")
	c.sess = &stubSession{}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := c.RconLogin(ctx, "pw"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want DeadlineExceeded, got %v", err)
	}
}
