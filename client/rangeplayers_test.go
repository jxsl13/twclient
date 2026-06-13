package client

import "testing"

// V51: RangePlayers visits every snapshot player and supports early stop.
func TestRangePlayers(t *testing.T) {
	c := benchClient() // 64 chars applied
	seen := 0
	c.RangePlayers(func(id int, ch CharacterState) bool {
		seen++
		return true
	})
	if seen != 64 {
		t.Errorf("visited %d players, want 64", seen)
	}

	// early stop after the first
	count := 0
	c.RangePlayers(func(id int, ch CharacterState) bool {
		count++
		return false
	})
	if count != 1 {
		t.Errorf("early-stop visited %d, want 1", count)
	}
}

// V51/V52: RangePlayers is zero-alloc (no result map, value yield) — the read
// path that avoids the per-tick charactersCopy.
func TestRangePlayersZeroAlloc(t *testing.T) {
	c := benchClient()
	allocs := testing.AllocsPerRun(100, func() {
		c.RangePlayers(func(int, CharacterState) bool { return true })
	})
	if allocs != 0 {
		t.Errorf("RangePlayers allocs/op = %v, want 0", allocs)
	}
}
