package ghost

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

// STREAM ≡ NONSTREAM: the streaming Decoder yields the exact same Chunk sequence
// as Parse, and a streaming re-encode is byte-identical to the input.
func TestStreamEqualsNonStream(t *testing.T) {
	orig := readTutorial(t)

	f, err := Parse(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	d, err := NewDecoder(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("new decoder: %v", err)
	}
	if !reflect.DeepEqual(d.Header(), f.Header) {
		t.Fatalf("decoder header != parse header")
	}

	var streamed []Chunk
	for {
		c, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next[%d]: %v", len(streamed), err)
		}
		streamed = append(streamed, c)
	}
	if len(streamed) != len(f.Chunks) {
		t.Fatalf("chunk count: stream=%d parse=%d", len(streamed), len(f.Chunks))
	}
	for i := range f.Chunks {
		if !reflect.DeepEqual(streamed[i], f.Chunks[i]) {
			t.Fatalf("chunk %d differs:\n stream=%#v\n parse =%#v", i, streamed[i], f.Chunks[i])
		}
	}

	// Streaming re-encode must reproduce the input byte-for-byte.
	var buf bytes.Buffer
	buf.Grow(len(orig))
	enc, err := NewEncoder(&buf, d.Header())
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	for i, c := range streamed {
		if err := enc.Write(c); err != nil {
			t.Fatalf("encode %d: %v", i, err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("encoder close: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), orig) {
		t.Fatalf("stream re-encode not byte-identical: in=%d out=%d first diff at %d",
			len(orig), buf.Len(), firstDiff(orig, buf.Bytes()))
	}
}

// The Decoder must report a clean io.EOF (not an error) at the end of the stream.
func TestDecoderCleanEOF(t *testing.T) {
	orig := readTutorial(t)
	d, err := NewDecoder(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("new decoder: %v", err)
	}
	for {
		_, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	// Further calls keep returning io.EOF.
	if _, err := d.Next(); err != io.EOF {
		t.Fatalf("post-EOF Next = %v, want io.EOF", err)
	}
}
