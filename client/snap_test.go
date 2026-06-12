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
