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
// streaming re-encode must reproduce the input byte-for-byte.
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

			// Streaming re-encode must reproduce the input byte-for-byte.
			var buf bytes.Buffer
			buf.Grow(len(orig))
			enc, err := NewEncoder(&buf, d.Header())
			if err != nil {
				t.Fatalf("new encoder: %v", err)
			}
			for i, ch := range streamed {
				if err := enc.Write(ch); err != nil {
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
