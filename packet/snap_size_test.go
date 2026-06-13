package packet

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
)

// V53: no option → default window (16). Existing behavior unchanged.
func TestNewSnapStorageDefaultMaxSnaps(t *testing.T) {
	if ss := NewSnapStorage(nil); ss.MaxSnaps != defaultMaxSnaps {
		t.Fatalf("default MaxSnaps = %d, want %d", ss.MaxSnaps, defaultMaxSnaps)
	}
}

// V53/V41: WithMaxSnaps validates in the ctor — valid values pass through,
// n <= 0 falls back to the default, and 0 < n < minMaxSnaps is clamped up so
// the delta base is never purged out from under the decoder.
func TestWithMaxSnapsClamp(t *testing.T) {
	cases := []struct{ in, want int }{
		{64, 64},
		{16, 16},
		{minMaxSnaps, minMaxSnaps},
		{minMaxSnaps - 1, minMaxSnaps},
		{1, minMaxSnaps},
		{0, defaultMaxSnaps},
		{-1, defaultMaxSnaps},
		{-100, defaultMaxSnaps},
	}
	for _, c := range cases {
		if ss := NewSnapStorage(nil, WithMaxSnaps(c.in)); ss.MaxSnaps != c.want {
			t.Errorf("WithMaxSnaps(%d): MaxSnaps = %d, want %d", c.in, ss.MaxSnaps, c.want)
		}
	}
}

// V53: a small (clamped) window decodes a delta stream identically to the
// default window as long as ack lag stays within it — i.e. shrinking the
// window is behavior-preserving for the supported case.
func TestSnapStorageSmallWindowDecodeParity(t *testing.T) {
	const n = 8
	emptyDelta := func() []byte {
		var b []byte
		b = append(b, packer.PackInt(0)...) // numDeleted
		b = append(b, packer.PackInt(0)...) // numUpdated
		b = append(b, packer.PackInt(0)...) // unused
		return b
	}

	big := NewSnapStorage(charSizeFn)                              // default 16
	small := NewSnapStorage(charSizeFn, WithMaxSnaps(minMaxSnaps)) // clamped floor (3)

	// Seed both with the same full snapshot (delta vs an empty base).
	seed := buildDelta(n)
	for _, ss := range []*SnapStorage{big, small} {
		if _, err := ss.ProcessSnap(100, 100, seed); err != nil {
			t.Fatalf("seed ProcessSnap: %v", err)
		}
	}

	// Carry-forward each tick (deltaTick=1, empty delta). The small window must
	// still hold the base it deltas against and produce identical snapshots.
	ed := emptyDelta()
	for tick := 101; tick <= 130; tick++ {
		sb, err := big.ProcessSnap(tick, 1, ed)
		if err != nil {
			t.Fatalf("big ProcessSnap tick %d: %v", tick, err)
		}
		sm, err := small.ProcessSnap(tick, 1, ed)
		if err != nil {
			t.Fatalf("small ProcessSnap tick %d: %v", tick, err)
		}
		if !snapEqual(sb, sm) {
			t.Fatalf("tick %d: small-window snapshot differs from default window", tick)
		}
	}
}
