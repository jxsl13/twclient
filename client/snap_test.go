package client

import (
	"testing"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/packet"
)

// charFields builds a SizeCharacter-length field slice with the given X.
func charFields(x int) []int {
	f := make([]int, net6.SizeCharacter)
	f[1] = x // X
	return f
}

func charSnap(tick int, ids ...int) *packet.Snapshot {
	s := &packet.Snapshot{Tick: tick}
	for _, id := range ids {
		s.Items = append(s.Items, packet.SnapItem{
			TypeID: net6.ObjCharacter,
			ID:     id,
			Fields: charFields(id * 10),
		})
	}
	return s
}

// charItem builds a character snap item with explicit hooked-player and weapon.
func charItem(id, hooked, weapon int) packet.SnapItem {
	f := make([]int, net6.SizeCharacter)
	f[1] = id * 10 // X
	f[8] = hooked  // HookedPlayer
	f[19] = weapon // Weapon
	return packet.SnapItem{TypeID: net6.ObjCharacter, ID: id, Fields: f}
}

func countEvents[E packet.Event](evs []packet.Event) int {
	n := 0
	for _, e := range evs {
		if _, ok := e.(E); ok {
			n++
		}
	}
	return n
}

// V12: SnapStorage tracks all players + a previous-snapshot copy for diffing.
func TestSnapStorageAllCharsAndPrev(t *testing.T) {
	var ss SnapStorage
	ss.localCID = 1

	// First snapshot: players 1 and 2 present.
	ss.processSnapshot(charSnap(100, 1, 2))
	if len(ss.characters) != 2 {
		t.Fatalf("want 2 chars, got %d", len(ss.characters))
	}
	if len(ss.prevCharacters) != 0 {
		t.Errorf("first snap: prevCharacters should be empty, got %d", len(ss.prevCharacters))
	}
	if ss.character.X != 10 {
		t.Errorf("local char X: want 10, got %d", ss.character.X)
	}

	// Second snapshot: player 2 left, player 3 joined.
	ss.processSnapshot(charSnap(101, 1, 3))
	if _, ok := ss.characters[2]; ok {
		t.Error("player 2 should be gone from current chars")
	}
	if _, ok := ss.characters[3]; !ok {
		t.Error("player 3 should be present in current chars")
	}
	// Previous map must still hold the first snapshot's set (1,2).
	if _, ok := ss.prevCharacters[2]; !ok {
		t.Error("prevCharacters should retain player 2 for diffing")
	}
	if _, ok := ss.prevCharacters[3]; ok {
		t.Error("prevCharacters should not yet contain player 3")
	}
}

// V5/V13: snap-derived core events — presence, hooked-by, weapon change.
func TestDeriveEventsCore(t *testing.T) {
	var ss SnapStorage
	ss.localCID = 1

	// Snap 1: local (1) and player 2 enter sight; no hook, weapon 0.
	s1 := &packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		charItem(1, 0, 0),
		charItem(2, 0, 0),
	}}
	ss.processSnapshot(s1)
	ev := ss.deriveEvents()
	if got := countEvents[packet.EventPlayerEnterSight](ev); got != 2 {
		t.Errorf("snap1: want 2 enter-sight, got %d", got)
	}

	// Snap 2: same set; player 2 hooks local (1); local weapon 0 -> 5 (laser).
	s2 := &packet.Snapshot{Tick: 2, Items: []packet.SnapItem{
		charItem(1, 0, 5),
		charItem(2, 1, 0),
	}}
	ss.processSnapshot(s2)
	ev = ss.deriveEvents()
	if got := countEvents[packet.EventHookedBy](ev); got != 1 {
		t.Errorf("snap2: want 1 hooked-by, got %d (%+v)", got, ev)
	}
	if got := countEvents[packet.EventWeaponChange](ev); got != 1 {
		t.Errorf("snap2: want 1 weapon-change, got %d (%+v)", got, ev)
	}
	if got := countEvents[packet.EventPlayerEnterSight](ev); got != 0 {
		t.Errorf("snap2: want 0 enter-sight, got %d", got)
	}

	// Snap 3: player 2 leaves sight.
	s3 := &packet.Snapshot{Tick: 3, Items: []packet.SnapItem{charItem(1, 0, 5)}}
	ss.processSnapshot(s3)
	ev = ss.deriveEvents()
	if got := countEvents[packet.EventPlayerLeaveSight](ev); got != 1 {
		t.Errorf("snap3: want 1 leave-sight, got %d", got)
	}
}
