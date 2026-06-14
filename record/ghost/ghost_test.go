package ghost

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

const tutorialPath = "../../testdata/Tutorial.gho"

func readTutorial(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(tutorialPath)
	if err != nil {
		t.Skipf("Tutorial.gho absent: %v", err)
	}
	return b
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// Magic must match DDNet gs_aHeaderMarker.
func TestMagic(t *testing.T) {
	want := []byte{'T', 'W', 'G', 'H', 'O', 'S', 'T', 0}
	if !bytes.Equal(Magic[:], want) {
		t.Fatalf("Magic = % x, want % x", Magic[:], want)
	}
}

// The Tutorial header parses with sane, expected values, including the v6-only
// SHA256.
func TestHeader(t *testing.T) {
	orig := readTutorial(t)
	f, err := Parse(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	h := f.Header
	if h.Version != 6 {
		t.Errorf("version = %d, want 6", h.Version)
	}
	if h.Map != "Tutorial" {
		t.Errorf("map = %q, want Tutorial", h.Map)
	}
	if h.Owner == "" {
		t.Errorf("owner empty")
	}
	if h.NumTicks <= 0 || h.Time <= 0 {
		t.Errorf("ticks=%d time=%d, want >0", h.NumTicks, h.Time)
	}
	// v6 zeroes the CRC word and carries a non-zero SHA256.
	if h.MapCRC != [4]byte{} {
		t.Errorf("v6 MapCRC = % x, want zero", h.MapCRC)
	}
	if h.MapSha256 == [Sha256Size]byte{} {
		t.Errorf("v6 MapSha256 is all zero")
	}
}

// BYTE-EQUAL whole-file: Parse then WriteTo reproduces Tutorial.gho exactly.
// THIS IS THE CRITICAL TEST.
func TestTutorialByteEqual(t *testing.T) {
	orig := readTutorial(t)
	f, err := Parse(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(f.Chunks) == 0 {
		t.Fatal("no chunks parsed")
	}
	var buf bytes.Buffer
	buf.Grow(len(orig))
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), orig) {
		t.Fatalf("round-trip not byte-identical: in=%d out=%d; first diff at %d",
			len(orig), buf.Len(), firstDiff(orig, buf.Bytes()))
	}
	t.Logf("%d chunks, %d bytes, version=%d", len(f.Chunks), len(orig), f.Header.Version)
}

// Fixpoint: Parse(WriteTo(Parse(x))) deep-equals Parse(x).
func TestReparseFixpoint(t *testing.T) {
	orig := readTutorial(t)
	f1, err := Parse(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if _, err := f1.WriteTo(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	f2, err := Parse(&buf)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !reflect.DeepEqual(f1, f2) {
		t.Fatalf("fixpoint mismatch: chunks %d vs %d", len(f1.Chunks), len(f2.Chunks))
	}
}

// Parse must never panic on hostile/malformed input; an error is fine.
func TestParseHostileNoPanic(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte("garbage"),
		bytes.Repeat([]byte{0xff}, 256),
		append(append([]byte{}, Magic[:]...), 0x06), // magic + version, truncated header
	}
	// bad magic, right length
	bad := make([]byte, 200)
	copy(bad, []byte("XWGHOST\x00"))
	cases = append(cases, bad)
	// unknown version
	unk := make([]byte, 200)
	copy(unk, Magic[:])
	unk[8] = 99
	cases = append(cases, unk)
	// valid v6 header + garbage/oversized chunk header
	if orig, err := os.ReadFile(tutorialPath); err == nil {
		f, _ := Parse(bytes.NewReader(orig))
		var hb bytes.Buffer
		f.WriteTo(&hb)
		head := hb.Bytes()[:133] // header only
		// oversized chunk size (0xffff > MaxChunkSize) then no data
		cases = append(cases, append(append([]byte{}, head...), 0x02, 0x01, 0xff, 0xff))
		// plausible size but truncated payload + garbage huffman
		cases = append(cases, append(append([]byte{}, head...), 0x02, 0x01, 0x00, 0x10, 0xde, 0xad))
	}

	for i, in := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("case %d panicked: %v", i, r)
				}
			}()
			_, _ = Parse(bytes.NewReader(in))
		}()
	}
}
