package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// V32: a client always has a stable, non-empty timeout code, and an explicit
// one is honored.
func TestTimeoutCodeStable(t *testing.T) {
	c := New("localhost:8303")
	code := c.TimeoutCode()
	if code == "" {
		t.Fatal("auto-generated timeout code must not be empty")
	}
	if c.TimeoutCode() != code {
		t.Error("timeout code must be stable across calls")
	}

	c2 := New("localhost:8303", WithTimeoutCode("fixedcode"))
	if c2.TimeoutCode() != "fixedcode" {
		t.Errorf("WithTimeoutCode: got %q", c2.TimeoutCode())
	}
}

// V32/V37: sendTimeoutCode emits "/timeout <code>" only on DDNet 0.6 with the
// chat-timeout-code capability; otherwise it is a no-op.
func TestSendTimeoutCodeGating(t *testing.T) {
	newClientWith := func(version packet.Version, chatCap bool) (*Client, *stubSession) {
		s := &stubSession{}
		c := New("localhost:8303", WithTimeoutCode("abc123"))
		c.sess = s
		c.version = version
		c.caps = packet.ServerCapabilities{ChatTimeoutCode: chatCap}
		return c, s
	}

	// DDNet 0.6 + capability → sent.
	c, s := newClientWith(packet.Version06, true)
	c.sendTimeoutCode()
	if s.lastCall != "chat" || s.chatMsg != "/timeout abc123" {
		t.Errorf("expected /timeout send, got call=%q msg=%q", s.lastCall, s.chatMsg)
	}

	// Capability absent → no send.
	c, s = newClientWith(packet.Version06, false)
	c.sendTimeoutCode()
	if s.lastCall == "chat" {
		t.Error("must not send timeout code without ChatTimeoutCode capability")
	}

	// 0.7 → no send (resume is 0.6-only, V37).
	c, s = newClientWith(packet.Version07, true)
	c.sendTimeoutCode()
	if s.lastCall == "chat" {
		t.Error("must not send timeout code on 0.7")
	}
}
