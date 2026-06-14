package teehistorian

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

func fixtures(t *testing.T) []string {
	t.Helper()
	m, err := filepath.Glob("../../testdata/teehistorian/*.teehistorian")
	if err != nil || len(m) == 0 {
		t.Skipf("no teehistorian fixtures: %v", err)
	}
	return m
}

// Magic must match DDNet CalculateUuid("teehistorian@ddnet.tw").
func TestMagic(t *testing.T) {
	want := []byte{0x69, 0x9d, 0xb1, 0x7b, 0x8e, 0xfb, 0x34, 0xff, 0xb1, 0xd8, 0xda, 0x6f, 0x60, 0xc1, 0x5d, 0xd1}
	if !bytes.Equal(Magic[:], want) {
		t.Fatalf("Magic = % x, want % x", Magic[:], want)
	}
}

// Every real fixture must parse, and re-serialize BYTE-IDENTICALLY (V77).
func TestRoundTripFixtures(t *testing.T) {
	for _, path := range fixtures(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(f.Records) == 0 {
				t.Fatal("no records parsed")
			}
			var buf bytes.Buffer
			if _, err := f.WriteTo(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), orig) {
				t.Fatalf("round-trip not byte-identical: in=%d out=%d; first diff at %d",
					len(orig), buf.Len(), firstDiff(orig, buf.Bytes()))
			}
			// Header typed view decoded something.
			if f.Header.Version == "" {
				t.Errorf("header version empty (raw=%.60s)", f.Header.Raw)
			}
		})
	}
}

// Re-parsing a written file yields an equal record count (semantic fixpoint).
func TestReparseFixpoint(t *testing.T) {
	for _, path := range fixtures(t) {
		orig, _ := os.ReadFile(path)
		f1, err := Parse(bytes.NewReader(orig))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var buf bytes.Buffer
		f1.WriteTo(&buf)
		f2, err := Parse(&buf)
		if err != nil {
			t.Fatalf("%s reparse: %v", path, err)
		}
		if len(f1.Records) != len(f2.Records) {
			t.Fatalf("%s: records %d != %d", path, len(f1.Records), len(f2.Records))
		}
	}
}

func firstDiff(a, b []byte) int {
	n := min(len(b), len(a))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// Ticks yields advancing ticks with player positions, for ML datasets.
func TestTicks(t *testing.T) {
	for _, path := range fixtures(t) {
		orig, _ := os.ReadFile(path)
		f, err := Parse(bytes.NewReader(orig))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		hasPlayers := false
		for _, r := range f.Records {
			switch r.(type) {
			case PlayerNew, PlayerDiff:
				hasPlayers = true
			}
		}
		var n, lastTick int
		f.Ticks(func(tick int, players map[int]PlayerState, inputs map[int]packet.PlayerInput) bool {
			if n > 0 && tick < lastTick {
				t.Fatalf("%s: tick went backward %d<%d", path, tick, lastTick)
			}
			lastTick = tick
			n++
			return true
		})
		if hasPlayers && n == 0 {
			t.Fatalf("%s: has player records but yielded no ticks", path)
		}
	}
}

// Parse must never panic on hostile/malformed input (V70).
func TestParseHostileNoPanic(t *testing.T) {
	cases := [][]byte{
		nil, {}, []byte("garbage"), bytes.Repeat([]byte{0xff}, 256),
		append(append([]byte{}, Magic[:]...), []byte(`{"version":"2"}`)...), // no NUL, no body
	}
	// valid magic + header + NUL + truncated/garbage body
	tr := append(append([]byte{}, Magic[:]...), []byte(`{"v":"2"}`)...)
	tr = append(tr, 0, 0xff, 0xff, 0xff, 0x80)
	cases = append(cases, tr)

	for i, in := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("case %d panicked: %v", i, r)
				}
			}()
			_, _ = Parse(bytes.NewReader(in)) // error is fine; panic is not
		}()
	}
}
