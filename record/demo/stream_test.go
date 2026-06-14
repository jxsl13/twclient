package demo

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The streaming Decoder must yield the exact same chunk sequence as Parse, and a
// streaming re-encode must re-parse deep-equal to the original (V94).
func TestStreamEqualsNonStream(t *testing.T) {
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

			d, err := NewDecoder(bytes.NewReader(orig))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}
			var streamed []Chunk
			for {
				ch, err := d.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("next[%d]: %v", len(streamed), err)
				}
				streamed = append(streamed, ch)
			}

			if len(streamed) != len(f.Chunks) {
				t.Fatalf("chunk count: stream=%d parse=%d", len(streamed), len(f.Chunks))
			}
			for i := range f.Chunks {
				if !reflect.DeepEqual(streamed[i], f.Chunks[i]) {
					t.Fatalf("chunk %d differs:\n stream=%#v\n parse =%#v", i, streamed[i], f.Chunks[i])
				}
			}

			// Streaming header must match the in-memory header.
			if !reflect.DeepEqual(d.Header(), f.Header) {
				t.Fatal("streaming header differs from parsed header")
			}

			// Streaming re-encode must re-parse deep-equal to the original.
			var buf bytes.Buffer
			enc, err := NewEncoder(&buf, d.Header())
			if err != nil {
				t.Fatalf("new encoder: %v", err)
			}
			for i, ch := range streamed {
				if err := enc.Write(ch); err != nil {
					t.Fatalf("encode %d: %v", i, err)
				}
			}
			reparsed, err := Parse(&buf)
			if err != nil {
				t.Fatalf("reparse stream re-encode: %v", err)
			}
			if !reflect.DeepEqual(reparsed.Header, f.Header) {
				t.Fatal("stream re-encode header differs after reparse")
			}
			if !reflect.DeepEqual(reparsed.Chunks, f.Chunks) {
				t.Fatal("stream re-encode chunks differ after reparse")
			}
		})
	}
}

// NewDecoder must not panic on hostile input.
func TestDecoderHostileNoPanic(t *testing.T) {
	cases := [][]byte{
		nil, {}, []byte("garbage"), bytes.Repeat([]byte{0xff}, 200),
	}
	for i, in := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("case %d panicked: %v", i, r)
				}
			}()
			d, err := NewDecoder(bytes.NewReader(in))
			if err == nil && d != nil {
				for {
					if _, err := d.Next(); err != nil {
						break
					}
				}
			}
		}()
	}
}
