package net7

import "testing"

// SnapItemSize must return the teeworlds 0.7 sizeof(CNetObj_X)/4 field counts for
// registered types, and -1 (read inline size) for race/unknown types (V108/B9).
func TestSnapItemSize(t *testing.T) {
	known := map[int]int{
		ObjPlayerInput:   10,
		ObjProjectile:    6,
		ObjLaser:         5,
		ObjPickup:        3,
		ObjFlag:          3,
		ObjGameData:      3,
		ObjGameDataTeam:  2,
		ObjGameDataFlag:  4,
		ObjCharacterCore: 15,
		ObjCharacter:     22, // CharacterCore(15) + 7
		ObjPlayerInfo:    3,
		ObjSpectatorInfo: 4,
		ObjDeClientInfo:  8,
		ObjDeGameInfo:    5,
		ObjDeTuneParams:  1,
		ObjCommon:        2,
		ObjExplosion:     2,
		ObjSpawn:         2,
		ObjHammerHit:     2,
		ObjDeath:         3,
		ObjSoundWorld:    3,
		ObjDamage:        7,
	}
	for typeID, want := range known {
		if got := SnapItemSize(typeID); got != want {
			t.Errorf("SnapItemSize(%d) = %d, want %d", typeID, got, want)
		}
	}
	// Race objects + unknown types are NOT registered → inline size (-1).
	for _, typeID := range []int{0, ObjPlayerInfoRace, ObjGameDataRace, 25, 64, 999} {
		if got := SnapItemSize(typeID); got != -1 {
			t.Errorf("SnapItemSize(%d) = %d, want -1 (inline)", typeID, got)
		}
	}
}
