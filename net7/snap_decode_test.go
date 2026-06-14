package net7

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// seq returns a Fields slice [start, start+1, …] of length n, so each positional
// field has a distinct, predictable value for assertions.
func seq(start, n int) []int {
	f := make([]int, n)
	for i := range f {
		f[i] = start + i
	}
	return f
}

// TestDecodeSnapPlayerInfo verifies the 0.7 PlayerInfo mapping: the client id is
// the snapshot ITEM ID (not a field), Team stays 0 (Sv_Team only), Local is false
// (cannot be derived statelessly), and Score/Latency come from fields 1/2.
func TestDecodeSnapPlayerInfo(t *testing.T) {
	snap := &packet.Snapshot{
		Tick: 42,
		Items: []packet.SnapItem{
			// PlayerFlags=7, Score=1337, Latency=25; item id 5 is the client id.
			{TypeID: ObjPlayerInfo, ID: 5, Fields: []int{7, 1337, 25}},
		},
	}
	out := DecodeSnap(snap)
	if out.Tick != 42 {
		t.Fatalf("Tick = %d, want 42", out.Tick)
	}
	p, ok := out.Players[5]
	if !ok {
		t.Fatalf("Players[5] missing")
	}
	want := packet.Player{ClientID: 5, Local: false, Team: 0, Score: 1337, Latency: 25, PlayerFlags: 7}
	if p != want {
		t.Errorf("Player = %+v, want %+v", p, want)
	}
}

// TestDecodeSnapCharacter verifies CharacterCore maps positionally (15 ints) and
// the 0.7 tail (Health…AttackTick) lands in the right fields, while PlayerFlags
// stays 0 (0.7 Character has no PlayerFlags field; its 7th tail is TriggeredEvents).
func TestDecodeSnapCharacter(t *testing.T) {
	// f[0..14] = core (100..114), f[15..21] = Health..AttackTick, TriggeredEvents.
	f := seq(100, SizeCharacter)
	snap := &packet.Snapshot{
		Tick:  1,
		Items: []packet.SnapItem{{TypeID: ObjCharacter, ID: 3, Fields: f}},
	}
	out := DecodeSnap(snap)
	c, ok := out.Characters[3]
	if !ok {
		t.Fatalf("Characters[3] missing")
	}
	want := packet.Character{
		Tick: 100, X: 101, Y: 102, VelX: 103, VelY: 104, Angle: 105,
		Direction: 106, Jumped: 107, HookedPlayer: 108, HookState: 109,
		HookTick: 110, HookX: 111, HookY: 112, HookDx: 113, HookDy: 114,
		PlayerFlags: 0, // 0.7 has no PlayerFlags on the character
		Health:      115, Armor: 116, AmmoCount: 117, Weapon: 118,
		Emote: 119, AttackTick: 120,
	}
	if c != want {
		t.Errorf("Character = %+v, want %+v", c, want)
	}
}

// TestDecodeSnapGameData verifies GameData(6)→GameInfo game-state fields and the
// GameDataTeam(7)+GameDataFlag(8) merge into the single canonical GameData.
func TestDecodeSnapGameData(t *testing.T) {
	snap := &packet.Snapshot{
		Tick: 9,
		Items: []packet.SnapItem{
			// RoundStartTick=10, GameStateFlags=2, GameStateEndTick=99.
			{TypeID: ObjGameData, ID: 0, Fields: []int{10, 2, 99}},
			// TeamscoreRed=3, TeamscoreBlue=4.
			{TypeID: ObjGameDataTeam, ID: 0, Fields: []int{3, 4}},
			// FlagCarrierRed=6, FlagCarrierBlue=7, dropRed=0, dropBlue=0.
			{TypeID: ObjGameDataFlag, ID: 0, Fields: []int{6, 7, 0, 0}},
		},
	}
	out := DecodeSnap(snap)
	wantInfo := packet.GameInfo{RoundStartTick: 10, GameStateFlags: 2, GameStateEndTick: 99}
	if out.GameInfo != wantInfo {
		t.Errorf("GameInfo = %+v, want %+v", out.GameInfo, wantInfo)
	}
	wantData := packet.GameData{TeamscoreRed: 3, TeamscoreBlue: 4, FlagCarrierRed: 6, FlagCarrierBlue: 7}
	if out.GameData != wantData {
		t.Errorf("GameData = %+v, want %+v", out.GameData, wantData)
	}
}

// TestDecodeSnapPickup verifies the 0.7→0.6 pickup type split (PickupType_SevenToSix).
func TestDecodeSnapPickup(t *testing.T) {
	cases := []struct {
		name        string
		type7       int
		wantType    int
		wantSubtype int
	}{
		{"health", pickup7Health, powerupHealth, 0},
		{"armor", pickup7Armor, powerupArmor, 0},
		{"grenade", pickup7Grenade, powerupWeapon, weaponGrenade},
		{"shotgun", pickup7Shotgun, powerupWeapon, weaponShotgun},
		{"laser", pickup7Laser, powerupWeapon, weaponLaser},
		{"gun", pickup7Gun, powerupWeapon, weaponGun},
		{"hammer", pickup7Hammer, powerupWeapon, weaponHammer},
		{"ninja", pickup7Ninja, powerupNinja, weaponNinja},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := &packet.Snapshot{
				Items: []packet.SnapItem{{TypeID: ObjPickup, ID: 0, Fields: []int{11, 22, c.type7}}},
			}
			out := DecodeSnap(snap)
			if len(out.Pickups) != 1 {
				t.Fatalf("Pickups len = %d, want 1", len(out.Pickups))
			}
			got := out.Pickups[0]
			want := packet.Pickup{X: 11, Y: 22, Type: c.wantType, Subtype: c.wantSubtype}
			if got != want {
				t.Errorf("Pickup = %+v, want %+v", got, want)
			}
		})
	}
}

// TestDecodeSnapEntities verifies Projectile, Laser and Flag positional mapping.
func TestDecodeSnapEntities(t *testing.T) {
	snap := &packet.Snapshot{
		Items: []packet.SnapItem{
			// X=1,Y=2,VelX=3,VelY=4,Type=5,StartTick=6.
			{TypeID: ObjProjectile, ID: 8, Fields: []int{1, 2, 3, 4, 5, 6}},
			// X=10,Y=20,FromX=30,FromY=40,StartTick=50.
			{TypeID: ObjLaser, ID: 0, Fields: []int{10, 20, 30, 40, 50}},
			// X=7,Y=8,Team=1.
			{TypeID: ObjFlag, ID: 0, Fields: []int{7, 8, 1}},
		},
	}
	out := DecodeSnap(snap)
	if len(out.Projectiles) != 1 || out.Projectiles[0] != (packet.Projectile{ID: 8, X: 1, Y: 2, VelX: 3, VelY: 4, Type: packet.Weapon(5), StartTick: 6}) {
		t.Errorf("Projectiles = %+v", out.Projectiles)
	}
	if len(out.Lasers) != 1 || out.Lasers[0] != (packet.Laser{X: 10, Y: 20, FromX: 30, FromY: 40, StartTick: 50}) {
		t.Errorf("Lasers = %+v", out.Lasers)
	}
	if len(out.Flags) != 1 || out.Flags[0] != (packet.Flag{X: 7, Y: 8, Team: 1}) {
		t.Errorf("Flags = %+v", out.Flags)
	}
}

// TestDecodeSnapSpectatorInfo verifies the SpecMode→canonical SpectatorID mapping:
// a non-freeview mode keeps the id, freeview normalizes to -1 (0.6 SPEC_FREEVIEW).
func TestDecodeSnapSpectatorInfo(t *testing.T) {
	// SpecMode=SPEC_PLAYER(1), SpectatorID=4, X=5, Y=6.
	snap := &packet.Snapshot{
		Items: []packet.SnapItem{{TypeID: ObjSpectatorInfo, ID: 0, Fields: []int{1, 4, 5, 6}}},
	}
	out := DecodeSnap(snap)
	if out.Spectator != (packet.SpectatorInfo{SpectatorID: 4, X: 5, Y: 6}) {
		t.Errorf("Spectator = %+v, want id 4", out.Spectator)
	}

	// SpecMode=SPEC_FREEVIEW(0) → SpectatorID normalized to -1.
	snap = &packet.Snapshot{
		Items: []packet.SnapItem{{TypeID: ObjSpectatorInfo, ID: 0, Fields: []int{spec7Freeview, 4, 5, 6}}},
	}
	out = DecodeSnap(snap)
	if out.Spectator != (packet.SpectatorInfo{SpectatorID: -1, X: 5, Y: 6}) {
		t.Errorf("Spectator = %+v, want freeview id -1", out.Spectator)
	}
}

// TestDecodeSnapShortFields verifies that items shorter than their size constant
// are skipped rather than panicking (V70), and unknown type ids are ignored.
func TestDecodeSnapShortFields(t *testing.T) {
	snap := &packet.Snapshot{
		Tick: 7,
		Items: []packet.SnapItem{
			{TypeID: ObjCharacter, ID: 1, Fields: []int{1, 2, 3}},  // < SizeCharacter
			{TypeID: ObjPlayerInfo, ID: 2, Fields: []int{1}},       // < SizePlayerInfo
			{TypeID: ObjPickup, ID: 0, Fields: []int{1}},           // < SizePickup
			{TypeID: ObjGameDataFlag, ID: 0, Fields: []int{1, 2}},  // < SizeGameDataFlag
			{TypeID: ObjSpectatorInfo, ID: 0, Fields: []int{0, 1}}, // < SizeSpectatorInfo
			{TypeID: ObjPlayerInfoRace, ID: 0, Fields: []int{1}},   // unknown/ignored
			{TypeID: 9999, ID: 0, Fields: nil},                     // unknown/ignored
		},
	}
	// Must not panic.
	out := DecodeSnap(snap)
	if len(out.Characters) != 0 || len(out.Players) != 0 || len(out.Pickups) != 0 {
		t.Errorf("short items should be skipped: %+v", out)
	}
	if out.Tick != 7 {
		t.Errorf("Tick = %d, want 7", out.Tick)
	}
}

func TestDecodeSnapEvents07(t *testing.T) {
	snap := &packet.Snapshot{Tick: 5, Items: []packet.SnapItem{
		{TypeID: ObjExplosion, ID: 0, Fields: []int{100, 200}},
		{TypeID: ObjDamage, ID: 1, Fields: []int{10, 20, 3, 45, 1, 0, 0}},
	}}
	out := DecodeSnap(snap)
	if len(out.Events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(out.Events), out.Events)
	}
	if e, ok := out.Events[1].(packet.EventDamageInd); !ok || e.Angle != 45 {
		t.Fatalf("event[1] not EventDamageInd{Angle:45}: %+v", out.Events[1])
	}
}

func TestLocalIDFromClientInfo(t *testing.T) {
	s, err := NewSession("127.0.0.1:9999")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.LocalID() != -1 {
		t.Fatalf("LocalID before any ClientInfo = %d, want -1", s.LocalID())
	}
	// Sv_ClientInfo: cid, m_Local, team, name, clan, country, 6 skin parts.
	var d []byte
	d = append(d, packer.PackInt(7)...) // cid
	d = append(d, packer.PackInt(1)...) // m_Local = true
	d = append(d, packer.PackInt(0)...) // team
	d = append(d, packer.PackString("me")...)
	d = append(d, packer.PackString("")...)
	d = append(d, packer.PackInt(0)...) // country
	for range 6 {
		d = append(d, packer.PackString("standard")...)
	}
	s.processClientInfo(d)
	if s.LocalID() != 7 {
		t.Fatalf("LocalID after local ClientInfo = %d, want 7", s.LocalID())
	}
}
