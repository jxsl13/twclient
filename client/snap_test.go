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

// charItemFull builds a character item with all motion-relevant fields.
func charItemFull(id int, c CharacterState) packet.SnapItem {
	f := make([]int, net6.SizeCharacter)
	f[1] = c.X
	f[2] = c.Y
	f[6] = c.Direction
	f[7] = c.Jumped
	f[8] = c.HookedPlayer
	f[9] = c.HookState
	f[19] = c.Weapon
	f[20] = c.Emote
	f[21] = c.AttackTick
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

// helpers for game-state objects
func gameInfoItem(stateFlags int) packet.SnapItem {
	f := make([]int, net6.SizeGameInfo)
	f[1] = stateFlags // GameStateFlags
	return packet.SnapItem{TypeID: net6.ObjGameInfo, ID: 0, Fields: f}
}

func playerInfoItem(cid, score int) packet.SnapItem {
	f := make([]int, net6.SizePlayerInfo)
	f[1] = cid   // ClientID
	f[3] = score // Score
	return packet.SnapItem{TypeID: net6.ObjPlayerInfo, ID: cid, Fields: f}
}

func gameDataItem(red, blue int) packet.SnapItem {
	f := make([]int, net6.SizeGameData)
	f[2] = red  // FlagCarrierRed
	f[3] = blue // FlagCarrierBlue
	return packet.SnapItem{TypeID: net6.ObjGameData, ID: 0, Fields: f}
}

func specInfoItem(target int) packet.SnapItem {
	f := make([]int, net6.SizeSpectatorInfo)
	f[0] = target // SpectatorId
	return packet.SnapItem{TypeID: net6.ObjSpectatorInfo, ID: 0, Fields: f}
}

// T5d: game/flag/round events fire on change, not on first snapshot.
func TestDeriveEventsGame(t *testing.T) {
	var ss SnapStorage

	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		gameInfoItem(0),
		playerInfoItem(5, 0),
		gameDataItem(-3, -3),
		specInfoItem(-1),
	}})
	if ev := ss.deriveEvents(); len(ev) != 0 {
		// first snapshot establishes baselines; only enter-sight-like events
		// would appear, but there are no characters here.
		if n := countEvents[packet.EventRoundState](ev) +
			countEvents[packet.EventScoreChange](ev) +
			countEvents[packet.EventFlag](ev) +
			countEvents[packet.EventSpecTarget](ev); n != 0 {
			t.Fatalf("first snap should emit no game events, got %d", n)
		}
	}

	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{
		gameInfoItem(GameStateFlagPaused),
		playerInfoItem(5, 10),
		gameDataItem(7, -3),
		specInfoItem(3),
	}})
	ev := ss.deriveEvents()
	if got := countEvents[packet.EventRoundState](ev); got != 1 {
		t.Errorf("want 1 round-state, got %d", got)
	}
	if got := countEvents[packet.EventScoreChange](ev); got != 1 {
		t.Errorf("want 1 score-change, got %d", got)
	}
	if got := countEvents[packet.EventFlag](ev); got != 1 {
		t.Errorf("want 1 flag (red carrier changed), got %d", got)
	}
	if got := countEvents[packet.EventSpecTarget](ev); got != 1 {
		t.Errorf("want 1 spec-target, got %d", got)
	}
}

// V15a: on 0.6, ObjClientInfo presence drives the same join/leave events the
// 0.7 reader emits as messages.
func TestDeriveRosterJoinLeave(t *testing.T) {
	var ss SnapStorage
	ci := func(id int) packet.SnapItem {
		return packet.SnapItem{TypeID: net6.ObjClientInfo, ID: id, Fields: make([]int, net6.SizeClientInfo)}
	}

	// First snapshot establishes the baseline roster {1,2}; no events.
	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{ci(1), ci(2)}})
	if ev := ss.deriveEvents(); countEvents[packet.EventPlayerJoin](ev) != 0 {
		t.Errorf("first snapshot must not emit joins")
	}

	// Player 2 leaves, player 3 joins.
	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{ci(1), ci(3)}})
	ev := ss.deriveEvents()
	if got := countEvents[packet.EventPlayerJoin](ev); got != 1 {
		t.Errorf("want 1 join, got %d", got)
	}
	if got := countEvents[packet.EventPlayerLeave](ev); got != 1 {
		t.Errorf("want 1 leave, got %d", got)
	}
}

// V14: transient world-event objects + projectile/laser fired-once.
func TestDeriveEventsTransient(t *testing.T) {
	var ss SnapStorage

	// Snap 1: an explosion, a death, and a new projectile (id 100).
	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		{TypeID: net6.ObjExplosion, ID: 1, Fields: []int{50, 60}},
		{TypeID: net6.ObjDeath, ID: 2, Fields: []int{10, 20, 3}},
		{TypeID: net6.ObjProjectile, ID: 100, Fields: []int{0, 0, 1, 1, 4, 0}},
	}})
	ev := ss.deriveEvents()
	if got := countEvents[packet.EventExplosion](ev); got != 1 {
		t.Errorf("want 1 explosion, got %d", got)
	}
	if got := countEvents[packet.EventDeath](ev); got != 1 {
		t.Errorf("want 1 death, got %d", got)
	}
	if got := countEvents[packet.EventProjectileFired](ev); got != 1 {
		t.Errorf("snap1: want 1 projectile-fired, got %d", got)
	}

	// Snap 2: projectile 100 persists (no re-fire); projectile 101 is new.
	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{
		{TypeID: net6.ObjProjectile, ID: 100, Fields: []int{1, 1, 1, 1, 4, 0}},
		{TypeID: net6.ObjProjectile, ID: 101, Fields: []int{5, 5, 2, 0, 2, 0}},
	}})
	ev = ss.deriveEvents()
	if got := countEvents[packet.EventProjectileFired](ev); got != 1 {
		t.Errorf("snap2: want 1 projectile-fired (only new id), got %d", got)
	}

	// A damage indicator emits EventDamageInd.
	ss.processSnapshot(&packet.Snapshot{Tick: 3, Items: []packet.SnapItem{
		{TypeID: net6.ObjDamageIndicator, ID: 1, Fields: []int{30, 40, 128}},
	}})
	if got := countEvents[packet.EventDamageInd](ss.deriveEvents()); got != 1 {
		t.Errorf("want 1 damage-ind, got %d", got)
	}
}

// V13: per-player motion events (move throttled, jump, dir, attack, weapon,
// hook, emote).
func TestDeriveEventsMotion(t *testing.T) {
	var ss SnapStorage
	ss.localCID = 1

	base := CharacterState{X: 100, Y: 100}
	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		charItemFull(2, base),
	}})
	ss.deriveEvents() // enter-sight only

	// Player 2: moves far, jumps, changes dir, fires, swaps weapon, hooks, emotes.
	next := CharacterState{
		X: 200, Y: 100, // dx=100 >= threshold
		Direction:    1,
		Jumped:       1,
		Weapon:       3,
		Emote:        2,
		HookState:    1,
		HookedPlayer: 5,
		AttackTick:   10,
	}
	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{
		charItemFull(2, next),
	}})
	ev := ss.deriveEvents()

	for name, got := range map[string]int{
		"move":   countEvents[packet.EventPlayerMove](ev),
		"jump":   countEvents[packet.EventPlayerJump](ev),
		"dir":    countEvents[packet.EventPlayerDir](ev),
		"attack": countEvents[packet.EventPlayerAttack](ev),
		"weapon": countEvents[packet.EventPlayerWeapon](ev),
		"hook":   countEvents[packet.EventPlayerHook](ev),
		"emote":  countEvents[packet.EventPlayerEmote](ev),
	} {
		if got != 1 {
			t.Errorf("motion %s: want 1, got %d", name, got)
		}
	}

	// A sub-threshold move must not emit EventPlayerMove.
	tiny := next
	tiny.X = next.X + 1 // dx=1 < threshold
	ss.processSnapshot(&packet.Snapshot{Tick: 3, Items: []packet.SnapItem{
		charItemFull(2, tiny),
	}})
	ev = ss.deriveEvents()
	if got := countEvents[packet.EventPlayerMove](ev); got != 0 {
		t.Errorf("sub-threshold move should not emit, got %d", got)
	}
}
