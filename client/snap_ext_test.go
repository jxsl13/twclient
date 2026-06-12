package client

import (
	"encoding/binary"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// uuidToFields is the inverse of uuidFromFields, for building marker items.
func uuidToFields(u [16]byte) []int {
	f := make([]int, 4)
	for i := 0; i < 4; i++ {
		f[i] = int(int32(binary.BigEndian.Uint32(u[i*4:])))
	}
	return f
}

func marker(typeID int, u [16]byte) packet.SnapItem {
	return packet.SnapItem{TypeID: 0, ID: typeID, Fields: uuidToFields(u)}
}

// T4e2: DDNet ext snapshot objects resolve via their UUID markers and emit
// change-triggered events.
func TestDeriveExtDDNetCharacter(t *testing.T) {
	var ss SnapStorage
	const extType = offsetUUIDType // internal type id for DDNetCharacter

	// fields: flags,freezeEnd,jumps,tele,strongweak,jumpedTotal,ninja,freezeStart,tx,ty
	char := func(flags, freezeEnd, jumps int) packet.SnapItem {
		return packet.SnapItem{TypeID: extType, ID: 7,
			Fields: []int{flags, freezeEnd, jumps, -1, 0, 0, -1, 0, 0, 0}}
	}

	// Baseline snapshot — no events on first sighting.
	ss.processSnapshot(&packet.Snapshot{Tick: 100, Items: []packet.SnapItem{
		marker(extType, uuidDDNetCharacter),
		char(0, 0, 2),
	}})
	if ev := ss.deriveEvents(); countEvents[packet.EventFreeze](ev) != 0 {
		t.Fatalf("first snapshot must not emit freeze")
	}

	// Freeze begins (freezeEnd jumps to a future tick) + flags change.
	ss.processSnapshot(&packet.Snapshot{Tick: 101, Items: []packet.SnapItem{
		marker(extType, uuidDDNetCharacter),
		char(8, 250, 2),
	}})
	ev := ss.deriveEvents()
	if got := countEvents[packet.EventFreeze](ev); got != 1 {
		t.Errorf("want 1 freeze, got %d", got)
	}
	if got := countEvents[packet.EventPlayerFlags](ev); got != 1 {
		t.Errorf("want 1 player-flags, got %d", got)
	}
}

func TestDeriveExtDDNetPlayer(t *testing.T) {
	var ss SnapStorage
	const extType = offsetUUIDType + 1

	player := func(flags, auth int) packet.SnapItem {
		return packet.SnapItem{TypeID: extType, ID: 3, Fields: []int{flags, auth}}
	}

	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		marker(extType, uuidDDNetPlayer),
		player(0, 0),
	}})
	ss.deriveEvents() // baseline

	// Auth level rises (login) and AFK bit set.
	ss.processSnapshot(&packet.Snapshot{Tick: 2, Items: []packet.SnapItem{
		marker(extType, uuidDDNetPlayer),
		player(exPlayerFlagAfk, 2),
	}})
	ev := ss.deriveEvents()
	if got := countEvents[packet.EventPlayerAuth](ev); got != 1 {
		t.Errorf("want 1 player-auth, got %d", got)
	}
	if got := countEvents[packet.EventPlayerAfk](ev); got != 1 {
		t.Errorf("want 1 player-afk, got %d", got)
	}
}

func TestDeriveExtSpecCharAndFinish(t *testing.T) {
	var ss SnapStorage
	const specType = offsetUUIDType + 2
	const finishType = offsetUUIDType + 3

	ev := func() []packet.Event {
		return ss.deriveEvents()
	}

	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		marker(specType, uuidSpecChar),
		{TypeID: specType, ID: 0, Fields: []int{500, 600}},
		marker(finishType, uuidFinish),
		{TypeID: finishType, ID: 4, Fields: []int{0, 0}},
	}})
	got := ev()
	if countEvents[packet.EventSpecChar](got) != 1 {
		t.Errorf("want 1 spec-char")
	}
	if countEvents[packet.EventFinish](got) != 1 {
		t.Errorf("want 1 finish")
	}
}

// Unknown ext UUID markers are ignored.
func TestDeriveExtUnknownIgnored(t *testing.T) {
	var ss SnapStorage
	var bogus [16]byte
	bogus[0] = 0xAB
	ss.processSnapshot(&packet.Snapshot{Tick: 1, Items: []packet.SnapItem{
		marker(offsetUUIDType, bogus),
		{TypeID: offsetUUIDType, ID: 1, Fields: []int{1, 2}},
	}})
	if ev := ss.deriveEvents(); len(ev) != 0 {
		t.Errorf("unknown ext object should emit nothing, got %d", len(ev))
	}
}
