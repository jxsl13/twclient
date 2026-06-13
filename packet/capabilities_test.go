package packet

import "testing"

// V47: flags decode to the right capability booleans; absent flags stay false.
func TestParseServerCapabilities(t *testing.T) {
	caps := ParseServerCapabilities(1, ServerCapFlagDDNet|ServerCapFlagChatTimeoutCode)
	if !caps.DDNet || !caps.ChatTimeoutCode {
		t.Fatalf("DDNet+ChatTimeoutCode should be set: %+v", caps)
	}
	if caps.PingEx || caps.AllowDummy || caps.SyncWeaponInput || caps.AnyPlayerFlag {
		t.Fatalf("unset flags must stay false: %+v", caps)
	}
	if caps.Version != 1 {
		t.Errorf("version: want 1, got %d", caps.Version)
	}

	zero := ParseServerCapabilities(0, 0)
	if zero.DDNet || zero.ChatTimeoutCode {
		t.Errorf("no flags → all false: %+v", zero)
	}
}
