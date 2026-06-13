package network

import (
	"testing"
	"time"
)

// V63: the library reads no environment variables. A Dial with no options uses
// DefaultReadTimeout regardless of any env that a previous version honored
// (e.g. TW_TIMEOUT) — env-driven config is the caller's job via WithReadTimeout.
func TestDialIgnoresEnv(t *testing.T) {
	t.Setenv("TW_TIMEOUT", "1s") // would have overridden the timeout in the old code
	conn, err := Dial("127.0.0.1:34999")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if got := conn.ReadTimeout(); got != DefaultReadTimeout {
		t.Errorf("ReadTimeout = %v, want DefaultReadTimeout %v (env must be ignored)", got, DefaultReadTimeout)
	}
}

// V62: DefaultReadTimeout matches DDNet conn_timeout (100s) and is exported.
func TestDefaultReadTimeoutValue(t *testing.T) {
	if DefaultReadTimeout != 100*time.Second {
		t.Errorf("DefaultReadTimeout = %v, want 100s (DDNet conn_timeout)", DefaultReadTimeout)
	}
}
