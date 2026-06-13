package net7

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/teeworlds-go/huffman/v2"
)

// TestReaderAndSyncRecvNoBufferRace is a regression test for B7 (V75): the
// background readLoop and a synchronous receive (e.g. DownloadMap during a live
// reader) must NOT share a huffman decompress buffer. It drives both receive
// paths concurrently against a local UDP server that floods compressed packets.
//
// Run under -race. Against the buggy shared-buffer version (a single
// Session.huffBuf used by both parsePayload callers) the race detector fires on
// the concurrent DecompressTo writes. With the per-receiver buffers (V75) it is
// clean. Uses only the public API, so it compiles against both versions.
func TestReaderAndSyncRecvNoBufferRace(t *testing.T) {
	// Local UDP "server" that learns the client addr then floods it with a
	// compressed, chunk-less packet as fast as it can.
	srv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()

	compressed, err := huffman.Compress([]byte("regression payload for the shared huffman buffer race B7"))
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	// Compression flag set, zero chunks → decompresses then no chunk work.
	pkt := append((&Header{Flags: Flags{Compression: true}}).Pack(), compressed...)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	go func() {
		buf := make([]byte, 2048)
		_ = srv.SetReadDeadline(time.Now().Add(time.Second))
		_, caddr, err := srv.ReadFromUDP(buf) // learn client return addr
		if err != nil {
			return
		}
		for ctx.Err() == nil {
			if _, err := srv.WriteToUDP(pkt, caddr); err != nil {
				return
			}
		}
	}()

	s, err := NewSession(srv.LocalAddr().String())
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer s.Close()

	// Send something so the server learns our source port.
	if err := s.SendKeepAlive(); err != nil {
		t.Fatalf("keepalive: %v", err)
	}

	// Reader path: background goroutine decompresses into reader.huffBuf.
	s.StartReader(ctx)
	defer s.StopReader()

	// Synchronous path: hammer RecvAndAck (the buffer used by DownloadMap) on
	// this goroutine, concurrently with the reader. Both decompress.
	for ctx.Err() == nil {
		if _, _, err := s.RecvAndAck(ctx); err != nil {
			break // ctx deadline / cancel ends the run
		}
	}
}
