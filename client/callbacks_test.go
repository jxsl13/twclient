package client

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// V1/V2: a registered handler fires for its event type and receives the client.
func TestCallbackFires(t *testing.T) {
	c := &Client{}
	var got packet.EventChat
	var gotClient *Client
	c.OnChat(func(cl *Client, e packet.EventChat) {
		gotClient = cl
		got = e
	})

	c.callbacks.dispatch(c, packet.EventChat{ClientID: 7, Msg: "hi"})

	if gotClient != c {
		t.Error("handler did not receive the client")
	}
	if got.ClientID != 7 || got.Msg != "hi" {
		t.Errorf("handler got wrong event: %+v", got)
	}
}

// V17: handlers are isolated by concrete event type.
func TestCallbackTypeIsolation(t *testing.T) {
	c := &Client{}
	var chatCalls, bcastCalls int
	c.OnChat(func(*Client, packet.EventChat) { chatCalls++ })
	c.OnBroadcast(func(*Client, packet.EventBroadcast) { bcastCalls++ })

	c.callbacks.dispatch(c, packet.EventChat{})
	if chatCalls != 1 || bcastCalls != 0 {
		t.Fatalf("type isolation broken: chat=%d bcast=%d", chatCalls, bcastCalls)
	}
	c.callbacks.dispatch(c, packet.EventBroadcast{})
	if bcastCalls != 1 {
		t.Fatalf("broadcast handler not fired: %d", bcastCalls)
	}
}

// V7: unregister removes the handler and is idempotent (no panic on re-call).
func TestCallbackUnregisterIdempotent(t *testing.T) {
	c := &Client{}
	var calls int
	unreg := c.OnChat(func(*Client, packet.EventChat) { calls++ })

	c.callbacks.dispatch(c, packet.EventChat{})
	if calls != 1 {
		t.Fatalf("want 1 call before unregister, got %d", calls)
	}

	unreg()
	unreg() // second call must be a no-op, not a panic

	c.callbacks.dispatch(c, packet.EventChat{})
	if calls != 1 {
		t.Fatalf("handler fired after unregister: %d", calls)
	}
}

// V3: concurrent register/unregister/dispatch must not race or deadlock.
// Run with -race to validate the locking.
func TestCallbackConcurrency(t *testing.T) {
	c := &Client{}
	var fired int64
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unreg := On(c, func(*Client, packet.EventChat) {
				atomic.AddInt64(&fired, 1)
			})
			c.callbacks.dispatch(c, packet.EventChat{})
			unreg()
		}()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.callbacks.dispatch(c, packet.EventChat{})
		}()
	}
	wg.Wait()
	// No assertion on exact count (interleaving-dependent); the point is the
	// race detector and absence of deadlock.
	_ = fired
}
