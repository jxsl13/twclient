package net6

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packer"
)

// lossyMockServer is a minimal 0.6 (vanilla, no TKEN) server that drives the
// handshake/login state machine and DROPS the first dropN datagrams of each
// step before replying. It exercises login retransmission (V68, B6): a client
// that resends the pending step survives the loss and completes Login.
//
// Steps: CONNECT→CONNECTACCEPT, INFO→MAP_CHANGE, READY→CON_READY. startinfo +
// entergame need no reply.
func lossyMockServer(t *testing.T, dropN int) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("mock listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	chunkPkt := func(seq int, msg []byte) []byte {
		hdr := Header{Ack: 0, NumChunks: 1}
		return append(hdr.Pack(), WrapVitalChunk(msg, seq)...)
	}
	connAccept := func() []byte {
		// Vanilla 0.6.5 CONNECTACCEPT: ctrl msg + 4-byte server token (no TKEN).
		payload := append([]byte{MsgCtrlConnectAccept}, 0xDE, 0xAD, 0xBE, 0xEF)
		return BuildCtrlPacketNoToken(0, payload)
	}
	mapChange := func() []byte {
		msg := packer.PackMsgID(MsgSysMapChange, true)
		msg = append(msg, packer.PackString("dm1")...) // name
		msg = append(msg, packer.PackInt(0)...)        // crc
		msg = append(msg, packer.PackInt(0)...)        // size
		return chunkPkt(1, msg)
	}
	conReady := func() []byte { return chunkPkt(2, packer.PackMsgID(MsgSysConReady, true)) }

	go func() {
		buf := make([]byte, 64*1024)
		phase, cnt := 0, 0
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var hdr Header
			if hdr.Unpack(buf[:n]) != nil {
				continue
			}
			ctrl := hdr.Flags.Control
			isConnect := ctrl && n > hdr.Size() && buf[hdr.Size()] == MsgCtrlConnect
			isChunk := !ctrl && hdr.NumChunks > 0

			reply := func(pkt []byte) { _, _ = conn.WriteToUDP(pkt, from) }
			switch phase {
			case 0: // await CONNECT
				if isConnect {
					if cnt++; cnt > dropN {
						reply(connAccept())
						phase, cnt = 1, 0
					}
				}
			case 1: // await INFO
				if isChunk {
					if cnt++; cnt > dropN {
						reply(mapChange())
						phase, cnt = 2, 0
					}
				}
			case 2: // await READY
				if isChunk {
					if cnt++; cnt > dropN {
						reply(conReady())
						phase = 3
					}
				}
			}
		}
	}()
	return conn.LocalAddr().String()
}

// V68/B6: Login completes despite the first N packets of every step being
// dropped — the client retransmits the pending CONNECT/INFO/READY until the
// server replies, instead of aborting on the first read timeout.
func TestLoginSurvivesPacketLoss(t *testing.T) {
	addr := lossyMockServer(t, 2) // drop the first 2 datagrams of each step

	s, err := NewSession(addr)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := s.Login(ctx, "tester", ""); err != nil {
		t.Fatalf("Login should survive packet loss via retransmission: %v", err)
	}
	if s.MapName() != "dm1" {
		t.Errorf("map name = %q, want dm1 (MAP_CHANGE processed)", s.MapName())
	}
}

// Sanity: with zero loss the same mock completes Login immediately.
func TestLoginNoLoss(t *testing.T) {
	addr := lossyMockServer(t, 0)
	s, err := NewSession(addr)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := s.Login(ctx, "tester", ""); err != nil {
		t.Fatalf("Login (no loss): %v", err)
	}
}
