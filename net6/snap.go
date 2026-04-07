package net6

// Snap item field counts per 0.6 object type.
const (
	SizePlayerInput     = 10
	SizeProjectile      = 6
	SizeLaser           = 5
	SizePickup          = 4
	SizeFlag            = 3
	SizeGameInfo        = 8
	SizeGameData        = 4
	SizeCharacterCore   = 15
	SizeCharacter       = 22
	SizePlayerInfo      = 5
	SizeClientInfo      = 17
	SizeSpectatorInfo   = 3
	SizeCommon          = 2
	SizeExplosion       = 2
	SizeSpawn           = 2
	SizeHammerHit       = 2
	SizeDeath           = 3
	SizeSoundGlobal     = 3
	SizeSoundWorld      = 3
	SizeDamageIndicator = 3
)

// SnapItemSize returns the number of int fields for a known 0.6 snap item type,
// or -1 if unknown.
func SnapItemSize(typeID int) int {
	switch typeID {
	case ObjPlayerInput:
		return SizePlayerInput
	case ObjProjectile:
		return SizeProjectile
	case ObjLaser:
		return SizeLaser
	case ObjPickup:
		return SizePickup
	case ObjFlag:
		return SizeFlag
	case ObjGameInfo:
		return SizeGameInfo
	case ObjGameData:
		return SizeGameData
	case ObjCharacterCore:
		return SizeCharacterCore
	case ObjCharacter:
		return SizeCharacter
	case ObjPlayerInfo:
		return SizePlayerInfo
	case ObjClientInfo:
		return SizeClientInfo
	case ObjSpectatorInfo:
		return SizeSpectatorInfo
	case ObjCommon:
		return SizeCommon
	case ObjExplosion:
		return SizeExplosion
	case ObjSpawn:
		return SizeSpawn
	case ObjHammerHit:
		return SizeHammerHit
	case ObjDeath:
		return SizeDeath
	case ObjSoundGlobal:
		return SizeSoundGlobal
	case ObjSoundWorld:
		return SizeSoundWorld
	case ObjDamageIndicator:
		return SizeDamageIndicator
	default:
		return -1
	}
}
