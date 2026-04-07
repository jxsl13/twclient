package client

import (
	"github.com/jxsl13/tw-protocol/packer"
)

// PlayerInput represents the client input sent to the server (CNetObj_PlayerInput).
// These are the 10 int fields in obj_player_input.
type PlayerInput struct {
	Direction    int // -1=left, 0=none, 1=right
	TargetX      int // Aim target X (relative to player)
	TargetY      int // Aim target Y (relative to player)
	Jump         int // 1=jump pressed (hold for double jump)
	Fire         int // Incremented each tick while fire is held
	Hook         int // 1=hook active
	PlayerFlags  int // PLAYERFLAG_PLAYING=1, IN_MENU=2, CHATTING=4, SCOREBOARD=8, AIM=16
	WantedWeapon int // 1-6: hammer, gun, shotgun, grenade, laser, ninja
	NextWeapon   int // Weapon switch delta (+1)
	PrevWeapon   int // Weapon switch delta (-1)
}

// pack serializes the input into varint-encoded bytes (10 packed ints).
func (p *PlayerInput) pack() []byte {
	data := make([]byte, 0, p.inputSize())
	data = append(data, packer.PackInt(p.Direction)...)
	data = append(data, packer.PackInt(p.TargetX)...)
	data = append(data, packer.PackInt(p.TargetY)...)
	data = append(data, packer.PackInt(p.Jump)...)
	data = append(data, packer.PackInt(p.Fire)...)
	data = append(data, packer.PackInt(p.Hook)...)
	data = append(data, packer.PackInt(p.PlayerFlags)...)
	data = append(data, packer.PackInt(p.WantedWeapon)...)
	data = append(data, packer.PackInt(p.NextWeapon)...)
	data = append(data, packer.PackInt(p.PrevWeapon)...)
	return data
}

// inputSize returns the input size in the format expected by SysInput (10 fields × 4 bytes).
func (p *PlayerInput) inputSize() int {
	return 10 * 4
}

// Weapon IDs
const (
	WeaponHammer  = 0
	WeaponGun     = 1
	WeaponShotgun = 2
	WeaponGrenade = 3
	WeaponLaser   = 4
	WeaponNinja   = 5
)
