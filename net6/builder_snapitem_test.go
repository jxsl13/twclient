package net6

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// SnapItemSize maps every known 0.6 snap-object type to its field count; an
// unknown type yields -1 (V133).
func TestSnapItemSizeAllTypes(t *testing.T) {
	known := []int{
		ObjPlayerInput, ObjProjectile, ObjLaser, ObjPickup, ObjFlag,
		ObjGameInfo, ObjGameData, ObjCharacterCore, ObjCharacter, ObjPlayerInfo,
		ObjClientInfo, ObjSpectatorInfo, ObjCommon, ObjExplosion, ObjSpawn,
		ObjHammerHit, ObjDeath, ObjSoundGlobal, ObjSoundWorld, ObjDamageIndicator,
	}
	for _, id := range known {
		if SnapItemSize(id) <= 0 {
			t.Errorf("SnapItemSize(%d) <= 0, want a known size", id)
		}
	}
	if SnapItemSize(-99999) != -1 {
		t.Error("SnapItemSize(unknown) != -1")
	}
}

// The 0.6 packet builders are pure constructors; smoke-cover that each produces
// a non-empty packet (V133).
func TestPacketBuilders(t *testing.T) {
	var tok packet.Token
	builders := map[string][]byte{
		"BuildCtrlPacket":      BuildCtrlPacket(tok, 0, []byte{MsgCtrlKeepAlive}),
		"BuildConnect":         BuildConnect(tok),
		"BuildKeepAlive":       BuildKeepAlive(tok, 0),
		"BuildClose":           BuildClose(tok, 0, "bye"),
		"BuildChunkPacket":     BuildChunkPacket(tok, 0, 1, false, WrapVitalChunk(SysReady(), 1)),
		"BuildInfoPacket":      BuildInfoPacket(tok, 0, 1),
		"BuildReadyPacket":     BuildReadyPacket(tok, 0, 2),
		"BuildEnterGamePacket": BuildEnterGamePacket(tok, 0, 3),
		"BuildStartInfoPacket": BuildStartInfoPacket(tok, 0, 4, "n", "c", "default", -1),
	}
	for name, b := range builders {
		if len(b) == 0 {
			t.Errorf("%s produced empty bytes", name)
		}
	}
}
