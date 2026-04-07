package packer

import (
	"encoding/hex"
	"fmt"
	"testing"
)

func TestCalculateUUID(t *testing.T) {
	tests := []struct {
		name    string
		wantHex string
	}{
		// Computed from CalculateUUID matching DDNet's algorithm:
		// MD5(TEEWORLDS_NAMESPACE + name) with UUID v3 version/variant bits set.
		{"what-is@ddnet.tw", "245e50979fe039d6bf7d9a29e1691e4c"},
		{"it-is@ddnet.tw", "6954847e2e873603b56236da29ed1aca"},
		{"i-dont-know@ddnet.tw", "416911b5797333bf8d527bf01e519cf0"},
		{"clientver@ddnet.tw", "8c00130484613e478787f672b3835bd4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uuid := CalculateUUID(tt.name)
			got := hex.EncodeToString(uuid[:])

			// For clientver, just verify it's deterministic and well-formed
			if tt.wantHex != "" {
				if got != tt.wantHex {
					t.Errorf("CalculateUUID(%q) = %s, want %s", tt.name, got, tt.wantHex)
				}
			}

			// Verify UUID v3 format
			version := (uuid[6] >> 4) & 0x0f
			if version != 3 {
				t.Errorf("CalculateUUID(%q): version = %d, want 3", tt.name, version)
			}
			variant := (uuid[8] >> 6) & 0x03
			if variant != 2 { // binary 10 = RFC 4122
				t.Errorf("CalculateUUID(%q): variant = %d, want 2", tt.name, variant)
			}

			// Verify deterministic: calling again gives same result
			uuid2 := CalculateUUID(tt.name)
			if uuid != uuid2 {
				t.Errorf("CalculateUUID(%q) not deterministic", tt.name)
			}

			t.Logf("UUID(%q) = %s", tt.name, formatUUID(uuid))
		})
	}
}

func formatUUID(uuid [16]byte) string {
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(uuid[0:4]),
		hex.EncodeToString(uuid[4:6]),
		hex.EncodeToString(uuid[6:8]),
		hex.EncodeToString(uuid[8:10]),
		hex.EncodeToString(uuid[10:16]))
}
