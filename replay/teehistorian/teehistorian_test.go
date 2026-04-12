package teehistorian_test

import (
	"io"
	"os"
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/replay/teehistorian"
)

func writeVarint(buf []byte, pos int, v int) int {
	if v < 0 {
		v = ^v
		buf[pos] = byte(0x40 | (v & 0x3F))
	} else {
		buf[pos] = byte(v & 0x3F)
	}
	v >>= 6
	if v > 0 {
		buf[pos] |= 0x80
		pos++
		for v > 0 {
			buf[pos] = byte(v & 0x7F)
			v >>= 7
			if v > 0 {
				buf[pos] |= 0x80
			}
			pos++
		}
		return pos
	}
	return pos + 1
}

func TestTeehistorianLoader(t *testing.T) {
	uuid := [16]byte{
		0x69, 0x9d, 0xb1, 0x7b, 0x8e, 0xfb, 0x34, 0xff,
		0xb1, 0xd8, 0xda, 0x6f, 0x60, 0xc1, 0x5d, 0xd1,
	}
	jsonHeader := []byte(`{"version":"1","map":"test_map","game_type":"ddrace","server_name":"test"}`)

	buf := make([]byte, 4096)
	pos := 0

	copy(buf[pos:], uuid[:])
	pos += 16

	copy(buf[pos:], jsonHeader)
	pos += len(jsonHeader)
	buf[pos] = 0
	pos++

	// PLAYER_NEW(-3): cid=0, x=100, y=200
	pos = writeVarint(buf, pos, -3)
	pos = writeVarint(buf, pos, 0)
	pos = writeVarint(buf, pos, 100)
	pos = writeVarint(buf, pos, 200)

	// INPUT_NEW(-6): cid=0
	pos = writeVarint(buf, pos, -6)
	pos = writeVarint(buf, pos, 0)
	inputFields := []int{1, 50, -30, 1, 3, 0, 1, 2, 0, 0}
	for _, v := range inputFields {
		pos = writeVarint(buf, pos, v)
	}

	// INPUT_DIFF(-5): cid=0
	pos = writeVarint(buf, pos, -5)
	pos = writeVarint(buf, pos, 0)
	diffFields := []int{0, 10, 0, 0, 1, 0, 0, 0, 0, 0}
	for _, v := range diffFields {
		pos = writeVarint(buf, pos, v)
	}

	// FINISH(-1)
	pos = writeVarint(buf, pos, -1)

	tmpFile, err := os.CreateTemp("", "teehistorian_test_*.th")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.Write(buf[:pos]); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	loader, err := teehistorian.Open(tmpFile.Name(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer loader.Close()

	info := loader.Info()
	if info.Map != "test_map" {
		t.Errorf("map = %q, want test_map", info.Map)
	}

	f1, err := loader.NextInput()
	if err != nil {
		t.Fatalf("input 1: %v", err)
	}
	if f1.Input.Direction != packet.DirRight {
		t.Errorf("direction = %d, want DirRight", f1.Input.Direction)
	}
	if f1.Input.TargetX != 50 {
		t.Errorf("targetX = %d, want 50", f1.Input.TargetX)
	}
	if f1.Input.TargetY != -30 {
		t.Errorf("targetY = %d, want -30", f1.Input.TargetY)
	}
	if f1.Input.Jump != packet.JumpOn {
		t.Errorf("jump = %d, want JumpOn", f1.Input.Jump)
	}
	if f1.Input.Fire != 3 {
		t.Errorf("fire = %d, want 3", f1.Input.Fire)
	}
	if f1.Input.WantedWeapon != packet.WeaponGun {
		t.Errorf("weapon = %d, want WeaponGun", f1.Input.WantedWeapon)
	}

	f2, err := loader.NextInput()
	if err != nil {
		t.Fatalf("input 2: %v", err)
	}
	if f2.Input.Direction != packet.DirRight {
		t.Errorf("direction = %d, want DirRight", f2.Input.Direction)
	}
	if f2.Input.TargetX != 60 {
		t.Errorf("targetX = %d, want 60 (50+10)", f2.Input.TargetX)
	}
	if f2.Input.Fire != 4 {
		t.Errorf("fire = %d, want 4 (3+1)", f2.Input.Fire)
	}

	_, err = loader.NextInput()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}
