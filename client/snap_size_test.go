package client

import "testing"

// V53: WithSnapStorageSize records the configured window on the Client; unset
// leaves 0 (meaning "use the default" downstream). Propagation to the session
// reader's packet.SnapStorage.MaxSnaps is covered in net6/net7.
func TestWithSnapStorageSizeOption(t *testing.T) {
	if c := New("localhost:8303", WithSnapStorageSize(64)); c.snapStorageSize != 64 {
		t.Errorf("snapStorageSize = %d, want 64", c.snapStorageSize)
	}
	if c := New("localhost:8303"); c.snapStorageSize != 0 {
		t.Errorf("default snapStorageSize = %d, want 0 (use default downstream)", c.snapStorageSize)
	}
}
