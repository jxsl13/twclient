package client

import (
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// V38: OnDisconnect fires on CTRL_CLOSE with the classified reason.
func TestOnDisconnectFires(t *testing.T) {
	c := New("localhost:8303")
	var got DisconnectReason
	calls := 0
	c.OnDisconnect(func(_ *Client, d DisconnectReason) {
		got = d
		calls++
	})

	c.handleEvent(packet.EventClose{Reason: "You have been banned for 3 minutes (x)"})

	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
	if got.Kind != DisconnectKindBanned || got.BanDuration != 3*time.Minute {
		t.Errorf("reason wrong: %+v", got)
	}
}

// V7: the unregister closure is idempotent and stops further delivery.
func TestOnDisconnectUnregister(t *testing.T) {
	c := New("localhost:8303")
	calls := 0
	remove := c.OnDisconnect(func(_ *Client, _ DisconnectReason) { calls++ })

	remove()
	remove() // idempotent: must not panic

	c.handleEvent(packet.EventClose{Reason: "Kicked (bye)"})
	if calls != 0 {
		t.Errorf("handler fired after unregister: calls=%d", calls)
	}
}

// V38: multiple handlers all fire.
func TestOnDisconnectMultiple(t *testing.T) {
	c := New("localhost:8303")
	a, b := 0, 0
	c.OnDisconnect(func(_ *Client, _ DisconnectReason) { a++ })
	c.OnDisconnect(func(_ *Client, _ DisconnectReason) { b++ })

	c.handleEvent(packet.EventClose{Reason: "Server shutdown"})
	if a != 1 || b != 1 {
		t.Errorf("both handlers should fire: a=%d b=%d", a, b)
	}
}
