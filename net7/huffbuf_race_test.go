package net7

import (
	"sync"
	"testing"

	"github.com/teeworlds-go/huffman/v2"
)

// TestParsePayloadConcurrentBuffers verifies the synchronous receive path
// (&s.huffBuf) and the readLoop path (&s.reader.huffBuf) can decompress
// concurrently without a data race: they own separate buffers and only read
// the shared, immutable s.huff (V75). Run with -race to be meaningful.
func TestParsePayloadConcurrentBuffers(t *testing.T) {
	s, err := NewSession("127.0.0.1:9999")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer s.Close()
	// readLoop normally allocates this in StartReader; do it directly since we
	// drive parsePayload by hand instead of running the loop.
	s.reader.huffBuf = make([]byte, 0, 1400)

	want := []byte("teeworlds 0.7 snapshot payload that should round-trip cleanly")
	compressed, err := huffman.Compress(want)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	resp := append((&Header{Flags: Flags{Compression: true}}).Pack(), compressed...)

	// Each buffer has a single owner goroutine (the real contract); the two
	// owners run concurrently. A buffer is never shared between goroutines.
	owner := func(buf *[]byte) {
		for i := 0; i < 2000; i++ {
			_, payload, perr := s.parsePayload(resp, buf)
			if perr != nil {
				t.Errorf("parsePayload: %v", perr)
				return
			}
			if string(payload) != string(want) {
				t.Errorf("payload mismatch: got %q want %q", payload, want)
				return
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); owner(&s.huffBuf) }()        // synchronous path
	go func() { defer wg.Done(); owner(&s.reader.huffBuf) }() // reader path
	wg.Wait()
}
