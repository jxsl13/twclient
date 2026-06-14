package demo

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fixturePaths returns the committed .demo fixtures, skipping cleanly if absent.
func fixturePaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, name := range []string{"Tutorial.demo", "Tutorial.client.demo"} {
		p := filepath.Join("../../testdata", name)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		t.Skip("no .demo fixtures available")
	}
	return paths
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

// Magic must be the DDNet gs_aHeaderMarker "TWDEMO\0".
func TestMagic(t *testing.T) {
	want := []byte{'T', 'W', 'D', 'E', 'M', 'O', 0}
	if !bytes.Equal(Magic[:], want) {
		t.Fatalf("Magic = % x, want % x", Magic[:], want)
	}
	if Version != 6 {
		t.Fatalf("Version = %d, want 6", Version)
	}
}

// The fixed header decodes the documented v6 fields.
func TestHeaderParse(t *testing.T) {
	for _, path := range fixturePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			h := f.Header
			if h.Version != 6 {
				t.Errorf("version = %d, want 6", h.Version)
			}
			if h.MapName != "Tutorial" {
				t.Errorf("map name = %q, want Tutorial", h.MapName)
			}
			if len(h.MapData) != int(h.MapSize) {
				t.Errorf("map data %d != map size %d", len(h.MapData), h.MapSize)
			}
			if h.Sha256 == nil {
				t.Errorf("expected SHA256 extension on v6 demo")
			}
			if len(f.Chunks) == 0 {
				t.Fatal("no chunks parsed")
			}
		})
	}
}

// CRITICAL: Parse then WriteTo must reproduce the input byte-for-byte.
func TestRoundTripByteEqual(t *testing.T) {
	for _, path := range fixturePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("parse: %v", err)
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
			t.Logf("%d chunks, %d bytes byte-identical", len(f.Chunks), len(orig))
		})
	}
}

// Parse(WriteTo(Parse(x))) deep-equals Parse(x): Header and Chunks are a fixpoint.
func TestReparseFixpoint(t *testing.T) {
	for _, path := range fixturePaths(t) {
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
			f1.WriteTo(&buf)
			f2, err := Parse(&buf)
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if !reflect.DeepEqual(f1.Header, f2.Header) {
				t.Fatalf("header differs after reparse")
			}
			if !reflect.DeepEqual(f1.Chunks, f2.Chunks) {
				t.Fatalf("chunks differ after reparse (%d vs %d)", len(f1.Chunks), len(f2.Chunks))
			}
		})
	}
}

// Every data chunk's payload must huffman-decompress without error, and the
// fixtures must contain real snapshot/delta chunks.
func TestDecompressChunks(t *testing.T) {
	for _, path := range fixturePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, _ := os.ReadFile(path)
			f, err := Parse(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var snaps, deltas, msgs, ticks int
			for i, ch := range f.Chunks {
				switch c := ch.(type) {
				case TickMarker:
					ticks++
				case DataChunk:
					if _, err := c.Decompress(); err != nil {
						t.Fatalf("chunk %d (%s) decompress: %v", i, c.Type, err)
					}
					if _, err := c.Ints(); err != nil {
						t.Fatalf("chunk %d (%s) ints: %v", i, c.Type, err)
					}
					switch c.Type {
					case ChunkTypeSnapshot:
						snaps++
					case ChunkTypeDelta:
						deltas++
					case ChunkTypeMessage:
						msgs++
					}
				}
			}
			if ticks == 0 {
				t.Error("no tick markers")
			}
			if snaps == 0 && deltas == 0 {
				t.Error("no snapshot or delta chunks")
			}
			t.Logf("ticks=%d snapshots=%d deltas=%d messages=%d", ticks, snaps, deltas, msgs)
		})
	}
}

// Tick markers must be monotonically non-decreasing (sanity on tick decoding).
func TestTicksMonotonic(t *testing.T) {
	for _, path := range fixturePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, _ := os.ReadFile(path)
			f, err := Parse(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			last := -1
			for _, ch := range f.Chunks {
				if tm, ok := ch.(TickMarker); ok {
					if last >= 0 && tm.Tick < last {
						t.Fatalf("tick went backward %d < %d", tm.Tick, last)
					}
					last = tm.Tick
				}
			}
		})
	}
}

// Parse must never panic on hostile/malformed input (error is fine, panic is not).
func TestParseHostileNoPanic(t *testing.T) {
	good := []byte{'T', 'W', 'D', 'E', 'M', 'O', 0, 6}
	cases := [][]byte{
		nil,
		{},
		[]byte("garbage"),
		bytes.Repeat([]byte{0xff}, 64),
		bytes.Repeat([]byte{0x00}, headerSize), // valid-ish zero header, no magic
		append(append([]byte{}, good...), bytes.Repeat([]byte{0}, 64)...), // truncated header
	}
	// Valid magic + version, header padded out, then a giant map size -> must error.
	huge := make([]byte, headerSize)
	copy(huge, Magic[:])
	huge[7] = 6
	huge[136], huge[137], huge[138], huge[139] = 0x7f, 0xff, 0xff, 0xff // MapSize ~2GB
	cases = append(cases, huge)
	// Valid preamble shape but garbage chunk bytes.
	bad := make([]byte, headerSize+timelineSize)
	copy(bad, Magic[:])
	bad[7] = 6
	bad = append(bad, 0x20, 0x1f) // data chunk claiming 31 bytes, none follow
	cases = append(cases, bad)

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
