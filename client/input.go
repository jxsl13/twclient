package client

import (
	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// packInput serializes a packet.PlayerInput into varint-encoded bytes (10 packed ints).
func packInput(p *packet.PlayerInput) []byte {
	fields := p.Raw()
	data := make([]byte, 0, inputSize)
	for _, v := range fields {
		data = append(data, packer.PackInt(v)...)
	}
	return data
}

// inputSize is the input size in the format expected by SysInput (10 fields × 4 bytes).
const inputSize = 10 * 4

// Weapon IDs (re-exported from packet for backward compatibility).
const (
	WeaponHammer  = packet.WeaponHammer
	WeaponGun     = packet.WeaponGun
	WeaponShotgun = packet.WeaponShotgun
	WeaponGrenade = packet.WeaponGrenade
	WeaponLaser   = packet.WeaponLaser
	WeaponNinja   = packet.WeaponNinja
)
