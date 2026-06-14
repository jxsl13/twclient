package client

import (
	"testing"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packet"
)

func playerInfoItemTeam(cid, team, score int) packet.SnapItem {
	f := make([]int, net6.SizePlayerInfo)
	f[1] = cid   // ClientID
	f[2] = team  // Team
	f[3] = score // Score
	return packet.SnapItem{TypeID: net6.ObjPlayerInfo, ID: cid, Fields: f}
}

func clientInfoItem(id, skinSeed int) packet.SnapItem {
	f := make([]int, net6.SizeClientInfo)
	for i := 8; i < 14; i++ { // skin ints [8:14]
		f[i] = skinSeed
	}
	return packet.SnapItem{TypeID: net6.ObjClientInfo, ID: id, Fields: f}
}

// T122: a 0.6 ObjPlayerInfo m_Team change emits EventTeamSet (change-only, like
// score). The 0.7 team feed is Sv_Team (net7) — together they give parity (V106).
func TestDeriveEventsTeam06(t *testing.T) {
	var ss SnapStorage
	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{playerInfoItemTeam(5, 0, 0)}})
	if got := countEvents[packet.EventTeamSet](ss.deriveEvents()); got != 0 {
		t.Fatalf("first snap should emit no team-set, got %d", got)
	}
	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{playerInfoItemTeam(5, 1, 0)}})
	ev := ss.deriveEvents()
	if got := countEvents[packet.EventTeamSet](ev); got != 1 {
		t.Fatalf("want 1 team-set after change, got %d", got)
	}
	for _, e := range ev {
		if ts, ok := e.(packet.EventTeamSet); ok && (ts.ClientID != 5 || ts.Team != 1) {
			t.Fatalf("bad team-set: %+v", ts)
		}
	}
}

// T123: a 0.6 ObjClientInfo skin change (for an already-present player) emits
// EventSkinChange. The 0.7 feed is Sv_SkinChange (net7) — parity (V106).
func TestDeriveEventsSkin06(t *testing.T) {
	var ss SnapStorage
	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{clientInfoItem(3, 100)}})
	_ = ss.deriveEvents() // baseline: present, no join (rosterInit just set)
	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{clientInfoItem(3, 200)}})
	ev := ss.deriveEvents()
	if got := countEvents[packet.EventSkinChange](ev); got != 1 {
		t.Fatalf("want 1 skin-change after change, got %d", got)
	}
	// Unchanged skin → no event.
	ss.processSnapshot(&packet.Snapshot{Tick: 3, Items: []packet.SnapItem{clientInfoItem(3, 200)}})
	if got := countEvents[packet.EventSkinChange](ss.deriveEvents()); got != 0 {
		t.Fatalf("unchanged skin should emit no skin-change, got %d", got)
	}
}

// --- T124: PARITY MATRIX — snapshot-derived state on BOTH protocols ---
//
// The net6-only-score bug (V106/B9) was that snap-derived score/character state
// only fired when keyed on net6 object ids. Post-T136 BOTH protocols decode
// through their own DecodeSnap into the shared packet.SnapObjects, so the SAME
// consumer derivation runs for 0.6 and 0.7. These tests build an equivalent
// snapshot per protocol and assert the shared derivations fire identically —
// the test that would have caught the net6-only feed.

// playerInfoItem07 builds a 0.7 ObjPlayerInfo: id == client id, fields are
// {m_PlayerFlags, m_Score, m_Latency} (Team is Sv_Team, not in the snapshot).
func playerInfoItem07(cid, score int) packet.SnapItem {
	f := make([]int, net7.SizePlayerInfo)
	f[1] = score
	return packet.SnapItem{TypeID: net7.ObjPlayerInfo, ID: cid, Fields: f}
}

// characterItem builds a Character item for the given object type id; the 0.6
// and 0.7 wire layouts are field-identical for the fields we assert (CharacterCore
// + tail), so one builder serves both with the protocol's ObjCharacter id.
func characterItem(typeID, cid, x, y int) packet.SnapItem {
	f := make([]int, net6.SizeCharacter) // == net7.SizeCharacter (22)
	f[1] = x
	f[2] = y
	return packet.SnapItem{TypeID: typeID, ID: cid, Fields: f}
}

// snapFor returns a SnapStorage wired to the given protocol's decoder, mirroring
// what client.Connect sets (V112). is07 also gates the 0.6-only names path.
func snapForProto(is07 bool) *SnapStorage {
	ss := &SnapStorage{localCID: -1}
	if is07 {
		ss.decode = net7.DecodeSnap
		ss.is07 = true
	} else {
		ss.decode = net6.DecodeSnap
	}
	return ss
}

// TestParityScoreChange asserts a per-player score change emits exactly one
// EventScoreChange on BOTH 0.6 and 0.7 — the regression the net6-only feed
// would fail (V106/V107).
func TestParityScoreChange(t *testing.T) {
	cases := []struct {
		name  string
		is07  bool
		first packet.SnapItem
		bump  packet.SnapItem
	}{
		{"0.6", false, playerInfoItemTeam(5, 0, 10), playerInfoItemTeam(5, 0, 20)},
		{"0.7", true, playerInfoItem07(5, 10), playerInfoItem07(5, 20)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ss := snapForProto(tc.is07)
			ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{tc.first}})
			if got := countEvents[packet.EventScoreChange](ss.deriveEvents()); got != 0 {
				t.Fatalf("first snap should emit no score-change, got %d", got)
			}
			ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{tc.bump}})
			ev := ss.deriveEvents()
			if got := countEvents[packet.EventScoreChange](ev); got != 1 {
				t.Fatalf("want 1 score-change after change, got %d", got)
			}
			for _, e := range ev {
				if sc, ok := e.(packet.EventScoreChange); ok && (sc.ClientID != 5 || sc.Score != 20) {
					t.Fatalf("bad score-change: %+v", sc)
				}
			}
		})
	}
}

// TestParityCharacterVisible asserts a character in the snapshot populates the
// per-client character map (so enter/leave-sight, hook, weapon derivations work)
// on BOTH protocols.
func TestParityCharacterVisible(t *testing.T) {
	cases := []struct {
		name   string
		is07   bool
		typeID int
	}{
		{"0.6", false, net6.ObjCharacter},
		{"0.7", true, net7.ObjCharacter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ss := snapForProto(tc.is07)
			ss.processSnapshot(&packet.Snapshot{
				Tick:  1,
				Items: []packet.SnapItem{characterItem(tc.typeID, 7, 100, 200)},
			})
			ev := ss.deriveEvents()
			if got := countEvents[packet.EventPlayerEnterSight](ev); got != 1 {
				t.Fatalf("want 1 enter-sight for the visible character, got %d", got)
			}
			ch, ok := ss.characters[7]
			if !ok {
				t.Fatal("character 7 missing from the per-client map")
			}
			if ch.X != 100 || ch.Y != 200 {
				t.Fatalf("character pos = (%d,%d), want (100,200)", ch.X, ch.Y)
			}
		})
	}
}

// TestParityPlayerFlags asserts CharacterState.PlayerFlags is populated on BOTH
// protocols (T125 finding): 0.6 carries the flags in the Character object; 0.7
// carries them in CNetObj_PlayerInfo, which the client overlays onto the
// character post-decode (V107/V115). Without the overlay, 0.7 would report 0.
func TestParityPlayerFlags(t *testing.T) {
	const wantFlags = 1 // PLAYERFLAG_PLAYING (bit 0)

	// 0.6: flags live in the Character object (tail field idx 15).
	char06 := characterItem(net6.ObjCharacter, 7, 100, 200)
	char06.Fields[15] = wantFlags
	ss6 := snapForProto(false)
	ss6.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{char06}})
	if got := ss6.characters[7].PlayerFlags; got != wantFlags {
		t.Fatalf("0.6 PlayerFlags = %d, want %d", got, wantFlags)
	}

	// 0.7: flags live in PlayerInfo (field idx 0); the client overlays them.
	pi07 := playerInfoItem07(7, 0)
	pi07.Fields[0] = wantFlags
	ss7 := snapForProto(true)
	ss7.processSnapshot(&packet.Snapshot{
		Tick:  1,
		Items: []packet.SnapItem{characterItem(net7.ObjCharacter, 7, 100, 200), pi07},
	})
	if got := ss7.characters[7].PlayerFlags; got != wantFlags {
		t.Fatalf("0.7 PlayerFlags = %d, want %d (PlayerInfo overlay missing)", got, wantFlags)
	}
}
