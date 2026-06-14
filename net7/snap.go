package net7

// Snap item field counts (number of int32 fields) per 0.7 object type. These are
// sizeof(CNetObj_X)/4 from teeworlds 0.7 generated protocol (CNetObjHandler::
// ms_aObjSizes, build/src/generated/protocol.cpp). Character embeds CharacterCore
// (15+7=22); events embed Common (2). See SnapItemSize.
const (
	SizePlayerInput   = 10
	SizeProjectile    = 6
	SizeLaser         = 5
	SizePickup        = 3
	SizeFlag          = 3
	SizeGameData      = 3
	SizeGameDataTeam  = 2
	SizeGameDataFlag  = 4
	SizeCharacterCore = 15
	SizeCharacter     = 22 // CharacterCore(15) + 7
	SizePlayerInfo    = 3
	SizeSpectatorInfo = 4
	SizeDeClientInfo  = 8
	SizeDeGameInfo    = 5
	SizeDeTuneParams  = 1
	SizeCommon        = 2
	SizeExplosion     = 2 // Common(2) + 0
	SizeSpawn         = 2
	SizeHammerHit     = 2
	SizeDeath         = 3 // Common(2) + 1
	SizeSoundWorld    = 3
	SizeDamage        = 7 // Common(2) + 5
)

// SnapItemSize returns the number of int32 fields for a known 0.7 snap object
// type, or -1 if the size is unknown and must be read from an inline size field
// in the snapshot delta (B9/V108).
//
// This mirrors teeworlds 0.7's registered size table: the server omits the
// inline per-item size for types it has registered (CSnapshot::m_aItemSizes),
// so the client must size them from the SAME table — otherwise it reads the
// next item field as a phantom size and desyncs (snapshot.cpp:357). The DDNet
// race objects (PlayerInfoRace=23, GameDataRace=24) are intentionally NOT
// registered (DDNet excludes them for forward-compat, and vanilla servers do
// not emit them), so they return -1 and carry an inline size.
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
	case ObjGameData:
		return SizeGameData
	case ObjGameDataTeam:
		return SizeGameDataTeam
	case ObjGameDataFlag:
		return SizeGameDataFlag
	case ObjCharacterCore:
		return SizeCharacterCore
	case ObjCharacter:
		return SizeCharacter
	case ObjPlayerInfo:
		return SizePlayerInfo
	case ObjSpectatorInfo:
		return SizeSpectatorInfo
	case ObjDeClientInfo:
		return SizeDeClientInfo
	case ObjDeGameInfo:
		return SizeDeGameInfo
	case ObjDeTuneParams:
		return SizeDeTuneParams
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
	case ObjSoundWorld:
		return SizeSoundWorld
	case ObjDamage:
		return SizeDamage
	default:
		return -1 // unknown / race objs (23,24) → inline size in the delta
	}
}
