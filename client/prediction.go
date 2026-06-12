package client

import (
	"sync"

	"github.com/jxsl13/twclient/packet"
)

// predInputRingSize is the number of recent inputs kept for prediction
// re-simulation — a few seconds at 50 ticks/s is ample to cover the gap
// between the acked tick and the predicted tick.
const predInputRingSize = 256

// predInput pairs a sent input with the predicted tick it was tagged for.
type predInput struct {
	tick  int
	input packet.PlayerInput
	set   bool
}

// predInputBuffer is a tick-keyed ring of recently sent local inputs. The
// prediction re-applies these from the acked tick forward to the predicted
// tick to reconstruct the local character's state (V9).
type predInputBuffer struct {
	mu   sync.Mutex
	ring [predInputRingSize]predInput
}

// record stores the input sent for predTick.
func (b *predInputBuffer) record(predTick int, input packet.PlayerInput) {
	if predTick <= 0 {
		return
	}
	b.mu.Lock()
	b.ring[predTick%predInputRingSize] = predInput{tick: predTick, input: input, set: true}
	b.mu.Unlock()
}

// get returns the input recorded for tick, if still present in the ring.
func (b *predInputBuffer) get(tick int) (packet.PlayerInput, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	slot := b.ring[tick%predInputRingSize]
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
