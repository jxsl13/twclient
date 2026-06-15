package net6

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// fullMockServer is a content-driven 0.6 mock: it inspects each datagram and
// replies with the matching handshake/login step, MAP_DATA on request, and a
// snapshot stream once in-game — enough to drive Login, DownloadMap and the
// background reader (readLoop → processSnap*) offline. Extends lossyMockServer
// (the existing login mock) for the download + reader coverage paths (V133).
func fullMockServer(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("mock listen: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	chunkPkt := func(seq int, msg []byte) []byte {
		hdr := Header{NumChunks: 1}
		return append(hdr.Pack(), WrapVitalChunk(msg, seq)...)
	}
	nonVitalPkt := func(msg []byte) []byte {
		hdr := Header{NumChunks: 1}
		return append(hdr.Pack(), WrapChunk(msg)...)
	}
	connAccept := func() []byte {
		return BuildCtrlPacketNoToken(0, append([]byte{MsgCtrlConnectAccept}, 0xDE, 0xAD, 0xBE, 0xEF))
	}
	mapChange := func() []byte {
		msg := packer.PackMsgID(MsgSysMapChange, true)
		msg = append(msg, packer.PackString("dm1")...)
		msg = append(msg, packer.PackInt(0)...)  // crc
		msg = append(msg, packer.PackInt(16)...) // size > 0 → DownloadMap proceeds
		return chunkPkt(1, msg)
	}
	mapData := func() []byte {
		msg := packer.PackMsgID(MsgSysMapData, true)
		msg = append(msg, packer.PackInt(1)...)  // last
		msg = append(msg, packer.PackInt(0)...)  // crc
		msg = append(msg, packer.PackInt(0)...)  // chunk
		msg = append(msg, packer.PackInt(4)...)  // size
		msg = append(msg, []byte{1, 2, 3, 4}...) // raw (not a valid map → parse fails, recv path covered)
		return chunkPkt(5, msg)
	}
	conReady := func() []byte { return chunkPkt(2, packer.PackMsgID(MsgSysConReady, true)) }
	snapEmpty := func(tick int) []byte {
		msg := packer.PackMsgID(MsgSysSnapEmpty, true)
		msg = append(msg, packer.PackInt(tick)...)
		msg = append(msg, packer.PackInt(-1)...) // delta from empty
		return nonVitalPkt(msg)
	}

	go func() {
		buf := make([]byte, 64*1024)
		var streaming bool
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var hdr Header
			if hdr.Unpack(buf[:n]) != nil {
				continue
			}
			reply := func(p []byte) { _, _ = conn.WriteToUDP(p, from) }
			if hdr.Flags.Control {
				if n > hdr.Size() && buf[hdr.Size()] == MsgCtrlConnect {
					reply(connAccept())
				}
				continue
			}
			payload := buf[hdr.Size():n]
			switch {
			case packet.ExtractSysMsgPayload(payload, MsgSysInfo, Split) != nil:
				reply(mapChange())
			case packet.ExtractSysMsgPayload(payload, MsgSysRequestMapData, Split) != nil:
				reply(mapData())
			case packet.ExtractSysMsgPayload(payload, MsgSysReady, Split) != nil:
				reply(conReady())
				if !streaming { // once in-game, stream snapshots proactively
					streaming = true
					go func(peer *net.UDPAddr) {
						for tick := 100; ; tick++ {
							if _, e := conn.WriteToUDP(snapEmpty(tick), peer); e != nil {
								return
							}
							time.Sleep(50 * time.Millisecond)
						}
					}(from)
				}
			}
		}
	}()
	return conn.LocalAddr().String()
}

// Drives Login → DownloadMap → background reader against the full mock, covering
// the recv/login/download/readLoop paths offline (V133).
func TestFullLoginDownloadReader(t *testing.T) {
	addr := fullMockServer(t)
	s, err := NewSession(addr)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	if err := s.Login(ctx, "tester", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if s.MapName() != "dm1" {
		t.Errorf("map = %q, want dm1", s.MapName())
	}

	// DownloadMap exercises the request/recv loop; the 4 garbage bytes are not a
	// valid map so Parse errors — the recv path (recvMapDataChunk) is the target.
	if _, err := s.DownloadMap(ctx); err == nil {
		t.Log("DownloadMap unexpectedly parsed the stub map data")
	}

	// Background reader: the mock streams snapshots → readLoop → processSnapEmpty
	// → EventSnapshot. Drain until one arrives.
	s.StartReader(ctx)
	t.Cleanup(s.StopReader)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-s.EventCh():
			if _, ok := ev.(packet.EventSnapshot); ok {
				return
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("no snapshot from the reader within 5s")
}
