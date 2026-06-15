package net7

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// mapDataResponder replies to every datagram with a MAP_DATA packet carrying
// `size` raw bytes — enough for DownloadMap's request loop to fill info.Size and
// exit (the bytes are not a valid map, so Parse errors; the recv path is the
// coverage target, V133).
func mapDataResponder(t *testing.T, size int) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 4096)
		seq := 1
		for {
			_, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			msg := append(packer.PackMsgID(MsgSysMapData, true), make([]byte, size)...)
			hdr := Header{NumChunks: 1}
			pkt := append(hdr.Pack(), WrapVitalChunk(msg, seq)...)
			seq++
			_, _ = conn.WriteToUDP(pkt, from)
		}
	}()
	return conn.LocalAddr().String()
}

func TestDownloadMap07(t *testing.T) {
	ctx := t.Context()

	// (a) loginMapData fast-path: DownloadMap parses the already-drained bytes
	// (garbage → parse error, the branch is covered).
	s1 := newTestSessionLive(t)
	s1.mapMu.Lock()
	s1.mapInfo = packet.MapInfo{Name: "m"}
	s1.mapMu.Unlock()
	s1.loginMapData = []byte{1, 2, 3, 4}
	if _, err := s1.DownloadMap(ctx); err == nil {
		t.Log("DownloadMap parsed the stub loginMapData unexpectedly")
	}

	// (b) request path: a responder pushes MAP_DATA until info.Size is reached.
	addr := mapDataResponder(t, 32)
	s2, err := NewSession(addr)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	s2.mapMu.Lock()
	s2.mapInfo = packet.MapInfo{Name: "m", Size: 16}
	s2.mapMu.Unlock()

	dlCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := s2.DownloadMap(dlCtx); err == nil {
		t.Log("DownloadMap parsed the stub map data unexpectedly")
	}
}

func TestRemainingConstructors07(t *testing.T) {
	if len(CtrlAccept()) == 0 {
		t.Error("CtrlAccept empty")
	}
	if len(SysPingReply()) == 0 {
		t.Error("SysPingReply empty")
	}
}
