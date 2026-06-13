package teehistorian

import (
	"bytes"
	"os"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// tutorialPath is a committed real human recording on the Tutorial map — small
// enough to track in git, exercised explicitly (it lives beside the other
// Tutorial assets in testdata/, NOT in the testdata/teehistorian/ glob dir).
const tutorialPath = "../testdata/Tutorial.teehistorian"

func readTutorial(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(tutorialPath)
	if err != nil {
		t.Skipf("Tutorial.teehistorian absent: %v", err)
	}
	return b
}

// The Tutorial recording parses, exposes a sane header, and re-serializes
// BYTE-IDENTICALLY (V77).
func TestTutorialRoundTrip(t *testing.T) {
	orig := readTutorial(t)
	f, err := Parse(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(f.Records) == 0 {
		t.Fatal("no records parsed")
	}
	if f.Header.Version == "" {
		t.Errorf("header version empty (raw=%.60s)", f.Header.Raw)
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
	t.Logf("%d records, %d bytes, version=%s", len(f.Records), len(orig), f.Header.Version)
}

// Re-parsing the written Tutorial file yields an equal record count (fixpoint).
func TestTutorialReparseFixpoint(t *testing.T) {
	orig := readTutorial(t)
	f1, err := Parse(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	f1.WriteTo(&buf)
	f2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if len(f1.Records) != len(f2.Records) {
		t.Fatalf("records %d != %d", len(f1.Records), len(f2.Records))
	}
}

// The Tutorial recording has real player movement, so Ticks must yield
// monotonic ticks with live players and per-tick inputs (dataset contract).
func TestTutorialTicks(t *testing.T) {
	orig := readTutorial(t)
	f, err := Parse(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var n, lastTick int
	sawPlayer := false
	f.Ticks(func(tick int, players map[int]PlayerState, inputs map[int]packet.PlayerInput) bool {
		if n > 0 && tick < lastTick {
			t.Fatalf("tick went backward %d<%d", tick, lastTick)
		}
		lastTick = tick
		if len(players) > 0 {
			sawPlayer = true
		}
		n++
		return true
	})
	if n == 0 {
		t.Fatal("yielded no ticks")
	}
	if !sawPlayer {
		t.Fatal("no tick ever had a live player")
	}
	t.Logf("%d ticks, last=%d", n, lastTick)
}
