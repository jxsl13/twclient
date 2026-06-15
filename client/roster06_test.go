package client

import (
	"testing"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/packet"
)

// strToInts mirrors DDNet StrToInts (4 chars/int, +128 per byte, low bit of the
// last int cleared) — to build ObjClientInfo snapshot fixtures.
func strToInts(s string, n int) []int {
	b := []byte(s)
	out := make([]int, n)
	bi := 0
	for i := range n {
		var v int
		for range 4 {
			ch := 0
			if bi < len(b) {
				ch = int(b[bi])
				bi++
			}
			v = (v << 8) | ((ch + 128) & 0xff)
		}
		out[i] = v
	}
	out[n-1] &^= 1
	return out
}

func clientInfoNamed(id int, name, clan string, country int, skin string) packet.SnapItem {
	f := make([]int, net6.SizeClientInfo)
	copy(f[0:4], strToInts(name, 4))
	copy(f[4:7], strToInts(clan, 3))
	f[7] = country
	copy(f[8:14], strToInts(skin, 6))
	return packet.SnapItem{TypeID: net6.ObjClientInfo, ID: id, Fields: f}
}

// Regression (issue #3, B24): on 0.6 the player registry was empty because the
// roster diff only emitted joins AFTER the first snapshot — every client present
// in snapshot #1 (dummies + existing players) was silently absorbed, never
// producing an EventPlayerJoin. The FIRST snapshot must already join them.
func TestDeriveRoster06InitialSnapshotJoins(t *testing.T) {
	var ss SnapStorage // is07 = false → 0.6 path; nil decode defaults to net6
	ss.localCID = 0

	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		clientInfoNamed(0, "alice", "ACL", 276, "default"),
		clientInfoNamed(1, "bob", "", -1, "santa"),
	}})
	evs := ss.deriveEvents()

	got := map[int]packet.EventPlayerJoin{}
	for _, e := range evs {
		if j, ok := e.(packet.EventPlayerJoin); ok {
			got[j.ClientID] = j
		}
	}
	if len(got) != 2 {
		t.Fatalf("first 0.6 snapshot emitted %d joins, want 2 (initial roster): %+v", len(got), evs)
	}
	if got[0].Name != "alice" || got[0].Clan != "ACL" || got[0].Country != 276 {
		t.Errorf("client 0 join wrong: %+v", got[0])
	}
	if got[1].Name != "bob" || got[1].Skin != "santa" {
		t.Errorf("client 1 join wrong: %+v", got[1])
	}

	// Second snapshot with the same clients → no duplicate joins.
	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{
		clientInfoNamed(0, "alice", "ACL", 276, "default"),
		clientInfoNamed(1, "bob", "", -1, "santa"),
	}})
	for _, e := range ss.deriveEvents() {
		if _, ok := e.(packet.EventPlayerJoin); ok {
			t.Errorf("duplicate join on unchanged second snapshot: %#v", e)
		}
	}
}
