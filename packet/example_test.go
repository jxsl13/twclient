package packet_test

import (
	"fmt"

	"github.com/jxsl13/twclient/packet"
)

// Build a player input the way a controller would: move right, jump, and select
// the grenade launcher. The typed enums (DirRight, JumpOn, WeaponGrenade) map
// 1:1 to the DDNet/Teeworlds CNetObj_PlayerInput fields.
func ExamplePlayerInput() {
	var in packet.PlayerInput
	_ = in.SetDirection(int(packet.DirRight))
	_ = in.SetJump(int(packet.JumpOn))
	_ = in.SetWantedWeapon(int(packet.WeaponGrenade))

	fmt.Println(in.Direction, in.Jump, in.WantedWeapon)
	// Output: 1 1 4
}

// Snapshot items carry absolute integer fields; SnapStorage applies the
// server's delta against a retained base. Here we just inspect a decoded item.
func ExampleSnapItem() {
	item := packet.SnapItem{TypeID: 9, ID: 0, Fields: []int{100, 200}}
	fmt.Printf("type=%d id=%d x=%d y=%d\n", item.TypeID, item.ID, item.Fields[0], item.Fields[1])
	// Output: type=9 id=0 x=100 y=200
}
