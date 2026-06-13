package client

import (
	"reflect"
	"sync"

	"github.com/jxsl13/twclient/packet"
)

// eventHandler is a type-erased callback. Registration wraps a typed
// func(*Client, E) into this shape; dispatch type-asserts back to E.
type eventHandler func(*Client, packet.Event)

// callbackRegistry maps a concrete event type to the set of handlers
// subscribed to it. It is safe for concurrent register/unregister/dispatch
// (V3): callers may (un)register from any goroutine while the event loop
// dispatches.
type callbackRegistry struct {
	mu       sync.RWMutex
	nextID   uint64
	handlers map[reflect.Type]map[uint64]eventHandler
}

// register adds h for event type t and returns an idempotent unregister
// closure (V7): the second and later calls are no-ops.
func (r *callbackRegistry) register(t reflect.Type, h eventHandler) func() {
	r.mu.Lock()
	if r.handlers == nil {
		r.handlers = make(map[reflect.Type]map[uint64]eventHandler)
	}
	if r.handlers[t] == nil {
		r.handlers[t] = make(map[uint64]eventHandler)
	}
	id := r.nextID
	r.nextID++
	r.handlers[t][id] = h
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if m := r.handlers[t]; m != nil {
				delete(m, id)
			}
			r.mu.Unlock()
		})
	}
}

// dispatch invokes every handler registered for ev's concrete type. Handlers
// are collected under a read lock and invoked after releasing it so a handler
// may (un)register or call back into the client without deadlocking (V2).
func (r *callbackRegistry) dispatch(c *Client, ev packet.Event) {
	r.mu.RLock()
	m := r.handlers[reflect.TypeOf(ev)]
	if len(m) == 0 {
		r.mu.RUnlock()
		return
	}
	hs := make([]eventHandler, 0, len(m))
	for _, h := range m {
		hs = append(hs, h)
	}
	r.mu.RUnlock()

	for _, h := range hs {
		h(c, ev)
	}
}

// On registers fn to be called for every event of concrete type E. It returns
// an unregister closure. This is the general per-event registrar; the OnXxx
// methods are thin wrappers for the most common events.
//
// Handlers run serially on the client's event-loop goroutine and receive the
// *Client, so they can respond inline (e.g. c.SendChat). A handler must return
// promptly or spawn its own goroutine — blocking stalls event delivery (V2).
func On[E packet.Event](c *Client, fn func(*Client, E)) (unregister func()) {
	var zero E
	t := reflect.TypeOf(zero)
	return c.callbacks.register(t, func(cl *Client, ev packet.Event) {
		fn(cl, ev.(E))
	})
}

// OnChat registers a handler for public/team chat lines.
func (c *Client) OnChat(fn func(*Client, packet.EventChat)) func() { return On(c, fn) }

// OnServerMsg registers a handler for server-authored chat messages.
func (c *Client) OnServerMsg(fn func(*Client, packet.EventServerMsg)) func() { return On(c, fn) }

// OnWhisper registers a handler for private (whisper) messages.
func (c *Client) OnWhisper(fn func(*Client, packet.EventWhisper)) func() { return On(c, fn) }

// OnBroadcast registers a handler for broadcast text.
func (c *Client) OnBroadcast(fn func(*Client, packet.EventBroadcast)) func() { return On(c, fn) }

// OnVoteSet registers a handler for vote start/clear.
func (c *Client) OnVoteSet(fn func(*Client, packet.EventVoteSet)) func() { return On(c, fn) }

// OnVoteStatus registers a handler for vote tally updates.
func (c *Client) OnVoteStatus(fn func(*Client, packet.EventVoteStatus)) func() { return On(c, fn) }

// OnKill registers a handler for kill messages.
func (c *Client) OnKill(fn func(*Client, packet.EventKill)) func() { return On(c, fn) }

// OnEmoticon registers a handler for other players' emoticons.
func (c *Client) OnEmoticon(fn func(*Client, packet.EventEmoticon)) func() { return On(c, fn) }

// OnHookedBy registers a handler for when another player hooks the local tee.
func (c *Client) OnHookedBy(fn func(*Client, packet.EventHookedBy)) func() { return On(c, fn) }

// OnWeaponChange registers a handler for when the server changes the local
// player's weapon.
func (c *Client) OnWeaponChange(fn func(*Client, packet.EventWeaponChange)) func() {
	return On(c, fn)
}

// OnServerCapabilities registers a handler for the DDNet server capabilities
// announcement (V47).
func (c *Client) OnServerCapabilities(fn func(*Client, packet.EventServerCapabilities)) func() {
	return On(c, fn)
}

// OnDisconnect registers a handler invoked when the server closes the
// connection, with the classified reason (V38). Handlers fire serially on the
// event-loop goroutine before any auto-reconnect attempt, so they must return
// promptly (or spawn their own goroutine). The returned closure unregisters the
// handler and is idempotent (V7).
func (c *Client) OnDisconnect(fn func(*Client, DisconnectReason)) func() {
	c.disconnectMu.Lock()
	if c.disconnectCbs == nil {
		c.disconnectCbs = make(map[uint64]func(*Client, DisconnectReason))
	}
	id := c.disconnectID
	c.disconnectID++
	c.disconnectCbs[id] = fn
	c.disconnectMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.disconnectMu.Lock()
			delete(c.disconnectCbs, id)
			c.disconnectMu.Unlock()
		})
	}
}

// fireDisconnect invokes all disconnect handlers with the classified reason.
// Handlers are snapshotted under the lock and called after release so a handler
// may (un)register or call back into the client (V2).
func (c *Client) fireDisconnect(reason DisconnectReason) {
	c.disconnectMu.Lock()
	if len(c.disconnectCbs) == 0 {
		c.disconnectMu.Unlock()
		return
	}
	hs := make([]func(*Client, DisconnectReason), 0, len(c.disconnectCbs))
	for _, h := range c.disconnectCbs {
		hs = append(hs, h)
	}
	c.disconnectMu.Unlock()

	for _, h := range hs {
		h(c, reason)
	}
}
