package client

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// V34: CTRL_CLOSE reasons classify into the right kind, ban durations parse,
// and the raw text is preserved verbatim.
func TestNewDisconnectReason(t *testing.T) {
	cases := []struct {
		raw     string
		kind    DisconnectKind
		banMins int // expected BanDuration in minutes, 0 = none
	}{
		{"", DisconnectKindClosed, 0},
		{"Kicked (spamming)", DisconnectKindKicked, 0},
		{"You have been banned for 5 minutes (flaming)", DisconnectKindBanned, 5},
		{"You have been banned for 1 minute (test)", DisconnectKindBanned, 1},
		{"You have been banned (cheating)", DisconnectKindBanned, 0},
		{"Kicked (your name is banned)", DisconnectKindBanned, 0},
		{"Wrong password", DisconnectKindWrongPassword, 0},
		{"Server shutdown", DisconnectKindShuttingDown, 0},
		{"This server is full", DisconnectKindFull, 0},
		{"Connection timed out", DisconnectKindTimedOut, 0},
		{"something weird", DisconnectKindUnknown, 0},
	}
	for _, c := range cases {
		r := NewDisconnectReason(c.raw)
		if r.Kind != c.kind {
			t.Errorf("%q: kind = %v, want %v", c.raw, r.Kind, c.kind)
		}
		if r.Text != c.raw {
			t.Errorf("%q: text not preserved verbatim, got %q", c.raw, r.Text)
		}
		want := time.Duration(c.banMins) * time.Minute
		if r.BanDuration != want {
			t.Errorf("%q: ban duration = %v, want %v", c.raw, r.BanDuration, want)
		}
	}
}

// V34: a CTRL_CLOSE event is classified and surfaced via LastDisconnect (the
// reason is no longer silently dropped).
func TestHandleEventClassifiesDisconnect(t *testing.T) {
	c := New("localhost:8303")
	c.handleEvent(packet.EventClose{Reason: "You have been banned for 10 minutes (afk)"})

	d := c.LastDisconnect()
	if d.Kind != DisconnectKindBanned {
		t.Fatalf("kind = %v, want Banned", d.Kind)
	}
	if d.BanDuration != 10*time.Minute {
		t.Errorf("ban duration = %v, want 10m", d.BanDuration)
	}
}

// V117: the login-rejection classifier (Client.Connect) inspects the error with
// the errors library, NOT a bare type assertion, so a *packet.ServerClosedError
// is recovered even when a layer wraps it with %w. A plain assertion would miss
// the wrapped form — this guards that the classifier keeps working if Login (or
// any intermediate) wraps the close error.
func TestServerClosedErrorClassifiedWhenWrapped(t *testing.T) {
	base := &packet.ServerClosedError{Reason: "Wrong password"}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"bare", base},
		{"wrapped", fmt.Errorf("session06: login: %w", base)},
		{"double-wrapped", fmt.Errorf("client: login: %w", fmt.Errorf("inner: %w", base))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// the exact pattern Connect uses (V117): errors.AsType recovers the
			// close error through any %w wrapping; a bare assertion would not.
			sce, ok := errors.AsType[*packet.ServerClosedError](tc.err)
			if !ok {
				t.Fatalf("AsType did not recover *ServerClosedError from %v", tc.err)
			}
			if got := NewDisconnectReason(sce.Reason); got.Kind != DisconnectKindWrongPassword {
				t.Fatalf("classified kind = %v, want WrongPassword", got.Kind)
			}
		})
	}
}
