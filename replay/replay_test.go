package replay_test

import (
	"io"
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/replay"
)

func TestCharacterToInputAdapter(t *testing.T) {
	frames := []replay.CharacterFrame{
		{Tick: 100, X: 1000, Y: 2000, Direction: 1, Weapon: replay.CharWeaponHammer},
		{Tick: 101, X: 1050, Y: 2000, Direction: 1, Weapon: replay.CharWeaponHammer},
		{Tick: 102, X: 1100, Y: 1900, Direction: 1, Weapon: replay.CharWeaponHammer, HookState: replay.CharHookRetractStart, HookX: 1200, HookY: 1700},
	}

	cp := &mockCharProvider{
		frames: frames,
		info:   replay.RecordingInfo{Format: "test", Map: "test_map"},
	}
	adapter := replay.NewCharacterToInputAdapter(cp)

	// First frame
	f1, err := adapter.NextInput()
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if f1.Tick != 100 {
		t.Errorf("tick = %d, want 100", f1.Tick)
	}

	// Second frame: moved right
	f2, err := adapter.NextInput()
	if err != nil {
		t.Fatalf("second frame: %v", err)
	}
	if f2.Tick != 101 {
		t.Errorf("tick = %d, want 101", f2.Tick)
	}
	if f2.Input.Direction != packet.DirRight {
		t.Errorf("direction = %d, want DirRight", f2.Input.Direction)
	}

	// Third frame: hook active
	f3, err := adapter.NextInput()
	if err != nil {
		t.Fatalf("third frame: %v", err)
	}
	if f3.Input.Hook != packet.HookOn {
		t.Errorf("hook = %d, want HookOn", f3.Input.Hook)
	}

	// EOF
	_, err = adapter.NextInput()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	// Info passthrough
	info := adapter.Info()
	if info.Format != "test" || info.Map != "test_map" {
		t.Errorf("info = %+v", info)
	}
}

type mockCharProvider struct {
	frames []replay.CharacterFrame
	info   replay.RecordingInfo
	pos    int
}

func (m *mockCharProvider) NextCharacter() (replay.CharacterFrame, error) {
	if m.pos >= len(m.frames) {
		return replay.CharacterFrame{}, io.EOF
	}
	f := m.frames[m.pos]
	m.pos++
	return f, nil
}

func (m *mockCharProvider) Info() replay.RecordingInfo { return m.info }
func (m *mockCharProvider) Close() error               { return nil }
