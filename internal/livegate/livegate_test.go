package livegate

import (
	"sync"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// 0.6 (and VersionAuto) pass straight through — no lock, no spacing.
func TestEnterNonZeroSevenNoOp(t *testing.T) {
	for _, v := range []packet.Version{packet.Version06, packet.VersionAuto} {
		start := time.Now()
		release := Enter(v)
		release()
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Errorf("Enter(%v) took %s, want immediate (no-op)", v, d)
		}
	}
}

// 0.7 connects are serialized (one at a time) and spaced by Spacing.
func TestEnterSevenSerializesAndSpaces(t *testing.T) {
	old := Spacing
	Spacing = 60 * time.Millisecond
	last = time.Time{} // reset package state for a deterministic run
	t.Cleanup(func() { Spacing = old; last = time.Time{} })

	const n = 4
	var (
		wg    sync.WaitGroup
		muT   sync.Mutex
		times []time.Time
	)
	for range n {
		wg.Go(func() {
			release := Enter(packet.Version07)
			muT.Lock()
			times = append(times, time.Now())
			muT.Unlock()
			release()
		})
	}
	wg.Wait()

	if len(times) != n {
		t.Fatalf("got %d entries, want %d", len(times), n)
	}
	// Consecutive 0.7 entries are spaced by at least ~Spacing (serialized).
	for i := 1; i < len(times); i++ {
		// times is appended under the gate, so it is already sorted by entry.
		if gap := times[i].Sub(times[i-1]); gap < Spacing-15*time.Millisecond {
			t.Errorf("gap %d→%d = %s, want >= ~%s (serialized+spaced)", i-1, i, gap, Spacing)
		}
	}
}
