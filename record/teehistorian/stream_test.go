package teehistorian

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// streamFixturePaths gathers every available teehistorian fixture: the committed
// small set, the gitignored large set (skipped cleanly if absent), and the
// committed Tutorial recording (skipped if absent).
func streamFixturePaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	small, _ := filepath.Glob("../../testdata/teehistorian/*.teehistorian")
	paths = append(paths, small...)
	large, _ := filepath.Glob("../../testdata/teehistorian-large/*.teehistorian")
	paths = append(paths, large...)
	if _, err := os.Stat(tutorialPath); err == nil {
		paths = append(paths, tutorialPath)
	}
	if len(paths) == 0 {
		t.Skip("no teehistorian fixtures available")
	}
	return paths
}

// TestStreamEqualsNonStream asserts STREAM ≡ NONSTREAM (V89): the streaming
// Decoder yields the exact same Record sequence as Parse (deep-equal, in order,
// same length), and a streaming re-encode is byte-identical to the input (V77
// on the streaming path). Files are processed one at a time to bound memory.
func TestStreamEqualsNonStream(t *testing.T) {
	for _, path := range streamFixturePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			// In-memory reference.
			f, err := Parse(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			// Streaming decode of the same bytes.
			d, err := NewDecoder(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}
			var streamed []Record
			for {
				rec, err := d.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("next[%d]: %v", len(streamed), err)
				}
				streamed = append(streamed, rec)
			}

			if len(streamed) != len(f.Records) {
				t.Fatalf("record count: stream=%d parse=%d", len(streamed), len(f.Records))
			}
			for i := range f.Records {
				if !reflect.DeepEqual(streamed[i], f.Records[i]) {
					t.Fatalf("record %d differs:\n stream=%#v\n parse =%#v", i, streamed[i], f.Records[i])
				}
			}

			// Streaming re-encode must reproduce the input byte-for-byte.
			var buf bytes.Buffer
			buf.Grow(len(orig))
			enc, err := NewEncoder(&buf, d.Header())
			if err != nil {
				t.Fatalf("new encoder: %v", err)
			}
			for i, rec := range streamed {
				if err := enc.Write(rec); err != nil {
					t.Fatalf("encode %d: %v", i, err)
				}
			}
			if !bytes.Equal(buf.Bytes(), orig) {
				t.Fatalf("stream re-encode not byte-identical: in=%d out=%d first diff at %d",
					len(orig), buf.Len(), firstDiff(orig, buf.Bytes()))
			}
		})
	}
}
