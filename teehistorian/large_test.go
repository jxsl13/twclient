package teehistorian

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// largeFixtures returns the big bot-generated recordings in the gitignored
// testdata/teehistorian-large/ folder. These are NOT committed (150MB+); the
// test skips cleanly when the folder is absent so CI and fresh clones pass.
func largeFixtures(t testing.TB) []string {
	m, _ := filepath.Glob("../testdata/teehistorian-large/*.teehistorian")
	if len(m) == 0 {
		t.Skip("no large teehistorian fixtures (testdata/teehistorian-large absent) — skipping")
	}
	return m
}

// Every large recording must round-trip BYTE-IDENTICALLY (V77), read one file
// at a time so peak memory stays at a single ~25MB file's working set.
func TestRoundTripLarge(t *testing.T) {
	for _, path := range largeFixtures(t) {
		path := path
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
			buf.Grow(len(orig))
			if _, err := f.WriteTo(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), orig) {
				t.Fatalf("round-trip not byte-identical: in=%d out=%d; first diff at %d",
					len(orig), buf.Len(), firstDiff(orig, buf.Bytes()))
			}
			t.Logf("%d records, %d bytes", len(f.Records), len(orig))
			// release before the next file so peak memory ~= one file.
			f, orig = nil, nil
			buf.Reset()
			runtime.GC()
		})
	}
}

// Re-parsing a written large file yields an equal record count (semantic
// fixpoint), one file at a time to bound memory.
func TestReparseLarge(t *testing.T) {
	for _, path := range largeFixtures(t) {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f1, err := Parse(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var buf bytes.Buffer
			buf.Grow(len(orig))
			f1.WriteTo(&buf)
			f2, err := Parse(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if len(f1.Records) != len(f2.Records) {
				t.Fatalf("records %d != %d", len(f1.Records), len(f2.Records))
			}
			f1, f2, orig = nil, nil, nil
			buf.Reset()
			runtime.GC()
		})
	}
}

// Ticks must replay each large recording to monotonic ticks without panic, and
// yield ticks whenever the file has player records (V70 + dataset contract).
func TestTicksLarge(t *testing.T) {
	for _, path := range largeFixtures(t) {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			hasPlayers := false
			for _, r := range f.Records {
				switch r.(type) {
				case PlayerNew, PlayerDiff:
					hasPlayers = true
				}
				if hasPlayers {
					break
				}
			}
			var n, lastTick int
			f.Ticks(func(tick int, _ map[int]PlayerState, _ map[int]packet.PlayerInput) bool {
				if n > 0 && tick < lastTick {
					t.Fatalf("tick went backward %d<%d", tick, lastTick)
				}
				lastTick = tick
				n++
				return true
			})
			if hasPlayers && n == 0 {
				t.Fatal("has player records but yielded no ticks")
			}
			t.Logf("%d ticks over %d records", n, len(f.Records))
			f, orig = nil, nil
			runtime.GC()
		})
	}
}

// BenchmarkParseLarge is the MEASURE-FIRST baseline for V85 (allocs/op, B/op,
// ns/op on a real large file). Skips when the folder is absent.
func BenchmarkParseLarge(b *testing.B) {
	paths := largeFixtures(b)
	orig, err := os.ReadFile(paths[0])
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := Parse(bytes.NewReader(orig))
		if err != nil {
			b.Fatal(err)
		}
		if len(f.Records) == 0 {
			b.Fatal("no records")
		}
	}
}
