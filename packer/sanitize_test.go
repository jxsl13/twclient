package packer

import (
	"math/rand"
	"testing"
)

// getStringSanitizedRef is the pre-T38 byte-by-byte reference. The fast-path
// implementation must match it for all inputs and flag combinations.
func getStringSanitizedRef(u *Unpacker, flags int) (string, error) {
	var buf []byte
	skipping := flags&SanitizeSkipWhitespaces != 0
	for {
		b, err := u.GetByte()
		if err != nil {
			return "", err
		}
		if b == 0 {
			break
		}
		if skipping {
			if b == ' ' || b == '\t' || b == '\n' {
				continue
			}
			skipping = false
		}
		if flags&SanitizeCC != 0 {
			if b < 32 {
				b = ' '
			}
		} else if flags&SanitizeDefault != 0 {
			if b < 32 && b != '\r' && b != '\n' && b != '\t' {
				b = ' '
			}
		}
		buf = append(buf, b)
	}
	return string(buf), nil
}

func TestGetStringSanitizedParity(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	flagSets := []int{
		0,
		SanitizeDefault,
		SanitizeCC,
		SanitizeSkipWhitespaces,
		SanitizeDefault | SanitizeSkipWhitespaces,
		SanitizeCC | SanitizeSkipWhitespaces,
	}
	for iter := range 5000 {
		n := rng.Intn(20)
		raw := make([]byte, n)
		for i := range raw {
			// Bias toward control chars, spaces, and printable to exercise paths.
			switch rng.Intn(4) {
			case 0:
				raw[i] = byte(rng.Intn(32)) // control (avoid NUL handled below)
			case 1:
				raw[i] = ' '
			default:
				raw[i] = byte(32 + rng.Intn(95))
			}
			if raw[i] == 0 {
				raw[i] = 1 // keep NUL only as the terminator
			}
		}
		data := append(append([]byte(nil), raw...), 0)
		flags := flagSets[rng.Intn(len(flagSets))]

		got, errG := NewUnpacker(data).GetStringSanitized(flags)
		want, errW := getStringSanitizedRef(NewUnpacker(data), flags)
		if (errG == nil) != (errW == nil) {
			t.Fatalf("iter %d: err mismatch g=%v w=%v", iter, errG, errW)
		}
		if got != want {
			t.Fatalf("iter %d flags=%d raw=%q: got %q want %q", iter, flags, raw, got, want)
		}
	}
}

func TestGetStringUnterminated(t *testing.T) {
	if _, err := NewUnpacker([]byte("no terminator")).GetStringSanitized(SanitizeDefault); err == nil {
		t.Fatal("expected error on unterminated string")
	}
}
