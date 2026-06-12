package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
)

// V25: one canonical type per concept; the physics weapon-id mirror is the sole
// permitted duplicate and must not drift from the canonical packet.Weapon.
func TestWeaponIdParity(t *testing.T) {
	cases := []struct {
		name    string
		canon   packet.Weapon
		physMir int
	}{
		{"gun", packet.WeaponGun, physics.WeaponGun},
		{"shotgun", packet.WeaponShotgun, physics.WeaponShotgun},
		{"grenade", packet.WeaponGrenade, physics.WeaponGrenade},
	}
	for _, c := range cases {
		if int(c.canon) != c.physMir {
			t.Errorf("weapon id drift (%s): packet=%d physics=%d", c.name, int(c.canon), c.physMir)
		}
	}
}

// V25: the predicted local character is the SAME CharacterState type as the
// snapshot one — no separate "PredictedCharacter" type. This is a compile-time
// guarantee; the assignment fails to build if the types ever diverge.
func TestPredictedCharacterIsCharacterState(t *testing.T) {
	c := &Client{}
	var _ CharacterState = c.PredictedCharacter()
	var _ map[int]CharacterState = c.PredictedCharacters()
}

// V25: input conversion has a single canonical site (inputToPhysics) mapping
// the wire PlayerInput to the sim Input. Spot-check the mapping is faithful.
func TestInputToPhysicsCanonical(t *testing.T) {
	in := packet.PlayerInput{
		Direction: packet.DirRight,
		TargetX:   5,
		TargetY:   -3,
		Jump:      packet.JumpOn,
		Hook:      packet.HookOn,
	}
	got := inputToPhysics(in)
	if got.Direction != 1 || got.TargetX != 5 || got.TargetY != -3 || !got.Jump || !got.Hook {
		t.Errorf("inputToPhysics mapping wrong: %#v", got)
	}
}
