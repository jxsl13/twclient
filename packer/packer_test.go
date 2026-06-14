package packer

import (
	"testing"
)

func TestPackUnpackInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		val  int
	}{
		{"zero", 0},
		{"one", 1},
		{"small", 42},
		{"boundary_63", 63},
		{"boundary_64", 64},
		{"medium", 1000},
		{"large", 100000},
		{"negative_one", -1},
		{"negative_small", -42},
		{"negative_large", -100000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			packed := PackInt(tt.val)
			got, err := UnpackInt(packed)
			if err != nil {
				t.Fatalf("UnpackInt error: %v", err)
			}
			if got != tt.val {
				t.Errorf("PackInt(%d) -> UnpackInt = %d", tt.val, got)
			}
		})
	}
}

func TestPackUnpackIntViaUnpacker(t *testing.T) {
	t.Parallel()
	vals := []int{0, 1, 63, 64, -1, -63, 1000, -1000}
	for _, v := range vals {
		packed := PackInt(v)
		u := NewUnpacker(packed)
		got, err := u.NextInt()
		if err != nil {
			t.Fatalf("NextInt error for %d: %v", v, err)
		}
		if got != v {
			t.Errorf("expected %d, got %d", v, got)
		}
	}
}

func TestPackUnpackString(t *testing.T) {
	t.Parallel()
	s := "hello world"
	packed := PackString(s)
	u := NewUnpacker(packed)
	got, err := u.NextString()
	if err != nil {
		t.Fatalf("NextString error: %v", err)
	}
	if got != s {
		t.Errorf("expected %q, got %q", s, got)
	}
}

func TestMsgAndSys(t *testing.T) {
	t.Parallel()
	// system message with ID 5
	packed := PackMsgID(5, true)
	u := NewUnpacker(packed)
	id, sys, err := u.NextMsgAndSys()
	if err != nil {
		t.Fatalf("NextMsgAndSys error: %v", err)
	}
	if id != 5 || !sys {
		t.Errorf("expected id=5 sys=true, got id=%d sys=%v", id, sys)
	}

	// game message with ID 3
	packed = PackMsgID(3, false)
	u = NewUnpacker(packed)
	id, sys, err = u.NextMsgAndSys()
	if err != nil {
		t.Fatalf("NextMsgAndSys error: %v", err)
	}
	if id != 3 || sys {
		t.Errorf("expected id=3 sys=false, got id=%d sys=%v", id, sys)
	}
}

func TestNextRaw(t *testing.T) {
	t.Parallel()
	data := []byte{0x01, 0x02, 0x03, 0x04}
	u := NewUnpacker(data)
	got, err := u.NextRaw(2)
	if err != nil {
		t.Fatalf("NextRaw error: %v", err)
	}
	if len(got) != 2 || got[0] != 0x01 || got[1] != 0x02 {
		t.Errorf("unexpected: %v", got)
	}
	if u.RemainingSize() != 2 {
		t.Errorf("expected 2 remaining, got %d", u.RemainingSize())
	}
}

func TestUnpackerEmpty(t *testing.T) {
	t.Parallel()
	u := NewUnpacker(nil)
	_, err := u.NextByte()
	if err == nil {
		t.Error("expected error on empty unpacker NextByte")
	}
	_, err = u.NextInt()
	if err == nil {
		t.Error("expected error on empty unpacker NextInt")
	}
}
