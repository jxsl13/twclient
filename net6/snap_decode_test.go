package net6

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// charFields returns a full 22-field 0.6 ObjCharacter layout with each slot set
// to its index, so a positional miswire is caught by an off-by-one value.
func charFields() []int {
	f := make([]int, SizeCharacter)
	for i := range f {
		f[i] = i
	}
	return f
}

func TestDecodeSnapCharacter(t *testing.T) {
	snap := &packet.Snapshot{
		Tick:  42,
		Items: []packet.SnapItem{{TypeID: ObjCharacter, ID: 3, Fields: charFields()}},
	}
	out := DecodeSnap(snap)

	if out.Tick != 42 {
		t.Fatalf("Tick = %d, want 42", out.Tick)
	}
	c, ok := out.Characters[3]
	if !ok {
		t.Fatalf("Characters[3] missing")
	}
	want := packet.Character{
		Tick: 0, X: 1, Y: 2, VelX: 3, VelY: 4, Angle: 5, Direction: 6,
		Jumped: 7, HookedPlayer: 8, HookState: 9, HookTick: 10, HookX: 11,
		HookY: 12, HookDx: 13, HookDy: 14, PlayerFlags: 15, Health: 16,
		Armor: 17, AmmoCount: 18, Weapon: 19, Emote: 20, AttackTick: 21,
	}
	if c != want {
		t.Errorf("Character = %+v, want %+v", c, want)
	}
}

func TestDecodeSnapPlayerInfo(t *testing.T) {
	// {Local, ClientID, Team, Score, Latency}: Local!=0 and item id differs from
	// the ClientID field to prove the field (not the item id) keys the map.
	snap := &packet.Snapshot{
		Items: []packet.SnapItem{{TypeID: ObjPlayerInfo, ID: 99, Fields: []int{1, 7, 1, 250, 42}}},
	}
	out := DecodeSnap(snap)

	p, ok := out.Players[7]
	if !ok {
		t.Fatalf("Players[7] missing (keyed by ClientID field)")
	}
	want := packet.Player{ClientID: 7, Local: true, Team: 1, Score: 250, Latency: 42}
	if p != want {
		t.Errorf("Player = %+v, want %+v", p, want)
	}
}

func TestDecodeSnapEntities(t *testing.T) {
	snap := &packet.Snapshot{
		Items: []packet.SnapItem{
			{TypeID: ObjProjectile, ID: 1, Fields: []int{10, 20, 30, 40, 2, 100}},
			{TypeID: ObjLaser, ID: 2, Fields: []int{1, 2, 3, 4, 5}},
			{TypeID: ObjPickup, ID: 3, Fields: []int{11, 12, 1, 0}},
			{TypeID: ObjFlag, ID: 4, Fields: []int{5, 6, 1}},
			{TypeID: ObjSpectatorInfo, ID: 5, Fields: []int{8, 100, 200}},
			{TypeID: ObjGameInfo, ID: 0, Fields: []int{1, 2, 3, 4, 5, 6, 7, 8}},
			{TypeID: ObjGameData, ID: 0, Fields: []int{3, 5, 0, 1}},
		},
	}
	out := DecodeSnap(snap)

	if len(out.Projectiles) != 1 || out.Projectiles[0] != (packet.Projectile{
		ID: 1, X: 10, Y: 20, VelX: 30, VelY: 40, Type: packet.Weapon(2), StartTick: 100,
	}) {
		t.Errorf("Projectiles = %+v", out.Projectiles)
	}
	if len(out.Lasers) != 1 || out.Lasers[0] != (packet.Laser{ID: 2, X: 1, Y: 2, FromX: 3, FromY: 4, StartTick: 5}) {
		t.Errorf("Lasers = %+v", out.Lasers)
	}
	if len(out.Pickups) != 1 || out.Pickups[0] != (packet.Pickup{X: 11, Y: 12, Type: 1, Subtype: 0}) {
		t.Errorf("Pickups = %+v", out.Pickups)
	}
	if len(out.Flags) != 1 || out.Flags[0] != (packet.Flag{X: 5, Y: 6, Team: 1}) {
		t.Errorf("Flags = %+v", out.Flags)
	}
	if out.Spectator != (packet.SpectatorInfo{SpectatorID: 8, X: 100, Y: 200}) {
		t.Errorf("Spectator = %+v", out.Spectator)
	}
	// GameInfo: {GameFlags,GameStateFlags,RoundStartTick,WarmupTimer,ScoreLimit,
	// TimeLimit,RoundNum,RoundCurrent}; GameStateEndTick has no 0.6 source.
	wantGI := packet.GameInfo{
		GameFlags: 1, GameStateFlags: 2, RoundStartTick: 3, WarmupTimer: 4,
		ScoreLimit: 5, TimeLimit: 6, RoundNum: 7, RoundCurrent: 8,
	}
	if out.GameInfo != wantGI {
		t.Errorf("GameInfo = %+v, want %+v", out.GameInfo, wantGI)
	}
	wantGD := packet.GameData{TeamscoreRed: 3, TeamscoreBlue: 5, FlagCarrierRed: 0, FlagCarrierBlue: 1}
	if out.GameData != wantGD {
		t.Errorf("GameData = %+v, want %+v", out.GameData, wantGD)
	}
}

// V70: short items must be skipped, not panic, and must not appear in the output.
func TestDecodeSnapShortItemNoPanic(t *testing.T) {
	snap := &packet.Snapshot{
		Items: []packet.SnapItem{
			{TypeID: ObjCharacter, ID: 1, Fields: []int{0, 1, 2}}, // short character
			{TypeID: ObjPlayerInfo, ID: 2, Fields: []int{1, 5}},   // short player info
			{TypeID: ObjLaser, ID: 3, Fields: []int{1, 2}},        // short laser
			{TypeID: 9999, ID: 4, Fields: []int{1, 2, 3}},         // unknown type id
		},
	}
	out := DecodeSnap(snap) // must not panic

	if len(out.Characters) != 0 {
		t.Errorf("Characters = %+v, want empty", out.Characters)
	}
	if len(out.Players) != 0 {
		t.Errorf("Players = %+v, want empty", out.Players)
	}
	if len(out.Lasers) != 0 {
		t.Errorf("Lasers = %+v, want empty", out.Lasers)
	}
}

func TestDecodeSnapEvents06(t *testing.T) {
	snap := &packet.Snapshot{Tick: 5, Items: []packet.SnapItem{
		{TypeID: ObjExplosion, ID: 0, Fields: []int{100, 200}},
		{TypeID: ObjDeath, ID: 1, Fields: []int{10, 20, 3}},
		{TypeID: ObjSoundWorld, ID: 2, Fields: []int{1, 2, 7}},
	}}
	out := DecodeSnap(snap)
	if len(out.Events) != 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(out.Events), out.Events)
	}
	if e, ok := out.Events[1].(packet.EventDeath); !ok || e.ClientID != 3 {
		t.Fatalf("event[1] not EventDeath{ClientID:3}: %+v", out.Events[1])
	}
}
