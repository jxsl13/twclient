package packet

import "testing"

// V42 + login defaults: ApplyLoginOptions falls back to DDNet/Teeworlds
// defaults and each option overrides its field.
func TestApplyLoginOptionsDefaults(t *testing.T) {
	cfg := ApplyLoginOptions()
	if cfg.Skin != DefaultSkin {
		t.Errorf("default skin: want %q, got %q", DefaultSkin, cfg.Skin)
	}
	if cfg.Country != DefaultCountry {
		t.Errorf("default country: want %d, got %d", DefaultCountry, cfg.Country)
	}
	if cfg.Password != "" {
		t.Errorf("default password: want empty, got %q", cfg.Password)
	}
}

func TestApplyLoginOptionsOverrides(t *testing.T) {
	cfg := ApplyLoginOptions(
		WithLoginSkin("santa"),
		WithLoginCountry(276),
		WithLoginPassword("s3cret"),
	)
	if cfg.Skin != "santa" || cfg.Country != 276 || cfg.Password != "s3cret" {
		t.Fatalf("options not applied: %+v", cfg)
	}
}
