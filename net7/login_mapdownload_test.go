package net7

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// mapGatedMockServer reproduces a real teeworlds 0.7 server's login gate (B11):
// it sends MAP_CHANGE, then sends MAP_DATA only in response to REQUEST_MAP_DATA,
// and sends CON_READY only AFTER the client has requested the map. A bare READY
// (no preceding REQUEST_MAP_DATA) is IGNORED — exactly the stall T130 diagnosed.
// So Login completes only if it downloads the map before READY (T131).
func mapGatedMockServer(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("mock listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	mapBytes := []byte("MAPDATA8") // 8 bytes; matches the size advertised below
	ctrlPkt := func(ctrlMsg byte, extra []byte) []byte {
		return append(append((&Header{Flags: Flags{Control: true}}).Pack(), ctrlMsg), extra...)
	}
	chunkPkt := func(seq int, msg []byte) []byte {
		return append((&Header{NumChunks: 1}).Pack(), WrapVitalChunk(msg, seq)...)
	}
	mapChange := func() []byte {
		msg := packer.PackMsgID(MsgSysMapChange, true)
		msg = append(msg, packer.PackString("dm1")...)
		msg = append(msg, packer.PackInt(0)...)             // crc
		msg = append(msg, packer.PackInt(len(mapBytes))...) // size
		msg = append(msg, packer.PackInt(1)...)             // chunks per request
		msg = append(msg, packer.PackInt(len(mapBytes))...) // chunk size
		msg = append(msg, make([]byte, 32)...)              // sha256
		return chunkPkt(1, msg)
	}
	mapData := func(seq int) []byte {
		return chunkPkt(seq, append(packer.PackMsgID(MsgSysMapData, true), mapBytes...))
	}
	conReady := func(seq int) []byte { return chunkPkt(seq, packer.PackMsgID(MsgSysConReady, true)) }

	// firstChunkMsg decodes the leading chunk's sys-message id from a packet.
	firstChunkMsg := func(buf []byte) (msgID int, ok bool) {
		var hdr Header
		if hdr.Unpack(buf) != nil || hdr.Flags.Control {
			return 0, false
		}
		chunks := packet.UnpackChunks(buf[HeaderSize:], Split)
		if len(chunks) == 0 || len(chunks[0].Data) < 1 {
			return 0, false
		}
		b := chunks[0].Data[0]
		raw := int(b & 0x3F)
		if b&0x40 != 0 {
			raw = ^raw
		}
		return raw >> 1, true // sys msg id (sys bit = raw&1)
	}

	go func() {
		buf := make([]byte, 64*1024)
		srvSeq := 1
		readyBeforeRequest := false // a bare READY arriving first is ignored (the bug)
		requested := false
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var hdr Header
			if hdr.Unpack(buf[:n]) != nil {
				continue
			}
			reply := func(pkt []byte) { _, _ = conn.WriteToUDP(pkt, from) }
			if hdr.Flags.Control && n > 7 {
				switch buf[7] {
				case MsgCtrlToken:
					reply(ctrlPkt(MsgCtrlToken, []byte{0xDE, 0xAD, 0xBE, 0xEF}))
				case MsgCtrlConnect:
					reply(ctrlPkt(MsgCtrlAccept, nil))
				}
				continue
			}
			msgID, ok := firstChunkMsg(buf[:n])
			if !ok {
				continue
			}
			switch msgID {
			case MsgSysInfo:
				reply(mapChange())
			case MsgSysRequestMapData:
				requested = true
				srvSeq++
				reply(mapData(srvSeq))
			case MsgSysReady:
				if !requested {
					readyBeforeRequest = true // gate: ignore bare READY (B11)
					continue
				}
				srvSeq++
				reply(conReady(srvSeq))
			}
			_ = readyBeforeRequest
		}
	}()
	return conn.LocalAddr().String()
}

// T131/V110: a 0.7 server that gates CON_READY behind the map-data exchange is
// satisfied only if Login downloads the map (REQUEST_MAP_DATA→MAP_DATA) before
// READY. With the pre-READY drain in place, Login completes.
func TestLoginDownloadsMapBeforeReady(t *testing.T) {
	addr := mapGatedMockServer(t)
	s, err := NewSession(addr)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := s.Login(ctx, "probe", ""); err != nil {
		t.Fatalf("login should complete via map download, got: %v", err)
	}
	if got := string(s.loginMapData); got != "MAPDATA8" {
		t.Fatalf("login did not drain the map: %q", got)
	}
}
