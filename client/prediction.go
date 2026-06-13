package client

import (
	"sync"

	"github.com/jxsl13/twclient/packet"
)

// defaultPredInputRingSize is the number of recent inputs kept for prediction
// re-simulation — a few seconds at 50 ticks/s is ample to cover the gap
// between the acked tick and the predicted tick (the original hardcoded value).
// minPredInputRingSize is the floor a configured ring is clamped up to, so the
// re-sim window from the acked tick forward is always covered (V54).
const (
	defaultPredInputRingSize = 256
	minPredInputRingSize     = 8
)

// clampPredInputRingSize validates a configured ring size (V41/V54): n <= 0
// falls back to the default, 0 < n < min is raised to the floor.
func clampPredInputRingSize(n int) int {
	switch {
	case n <= 0:
		return defaultPredInputRingSize
	case n < minPredInputRingSize:
		return minPredInputRingSize
	default:
		return n
	}
}

// predInput pairs a sent input with the predicted tick it was tagged for.
type predInput struct {
	tick  int
	input packet.PlayerInput
	set   bool
}

// predInputBuffer is a tick-keyed ring of recently sent local inputs. The
// prediction re-applies these from the acked tick forward to the predicted
// tick to reconstruct the local character's state (V9). The ring length is
// configurable (V54, WithPredInputRingSize); a zero-value buffer lazily sizes
// itself to the default so directly-constructed Clients stay safe.
type predInputBuffer struct {
	mu   sync.Mutex
	ring []predInput
}

// configure sizes the ring (clamped). Called once at Client construction.
func (b *predInputBuffer) configure(n int) {
	b.mu.Lock()
	b.ring = make([]predInput, clampPredInputRingSize(n))
	b.mu.Unlock()
}

// ensureRing lazily allocates the default ring; caller must hold b.mu.
func (b *predInputBuffer) ensureRing() {
	if len(b.ring) == 0 {
		b.ring = make([]predInput, defaultPredInputRingSize)
	}
}

// record stores the input sent for predTick.
func (b *predInputBuffer) record(predTick int, input packet.PlayerInput) {
	if predTick <= 0 {
		return
	}
	b.mu.Lock()
	b.ensureRing()
	b.ring[predTick%len(b.ring)] = predInput{tick: predTick, input: input, set: true}
	b.mu.Unlock()
}

// get returns the input recorded for tick, if still present in the ring.
func (b *predInputBuffer) get(tick int) (packet.PlayerInput, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureRing()
	slot := b.ring[tick%len(b.ring)]
	if slot.set && slot.tick == tick {
		return slot.input, true
	}
	return packet.PlayerInput{}, false
}

// LocalID returns the local player's client ID as learned from snapshots,
// or -1 if not yet known.
func (c *Client) LocalID() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.snap.characters == nil {
		// localCID defaults to 0; report -1 until a snapshot has been seen.
		if c.snap.lastSnap == nil {
			return -1
		}
	}
	return c.snap.localCID
}
