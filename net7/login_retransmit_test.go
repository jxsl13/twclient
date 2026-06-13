package net7

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packer"
)

// lossyMockServer is a minimal 0.7 server driving the handshake/login state
// machine, DROPPING the first dropN datagrams of each step before replying.
// Exercises login retransmission (V68, B6): the client resends the pending
// step (token request / CONNECT / INFO / READY) and completes Login despite
// the loss.
//
// Steps: TOKEN→TOKEN(server token), CONNECT→ACCEPT, INFO→MAP_CHANGE,
// READY→CON_READY. startinfo + entergame need no reply.
func lossyMockServer(t *testing.T, dropN int) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("mock listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	ctrlPkt := func(ctrlMsg byte, extra []byte) []byte {
		b := (&Header{Flags: Flags{Control: true}}).Pack() // 7-byte control header
		b = append(b, ctrlMsg)
		return append(b, extra...)
	}
	chunkPkt := func(seq int, msg []byte) []byte {
		b := (&Header{NumChunks: 1}).Pack()
		return append(b, WrapVitalChunk(msg, seq)...)
	}
	tokenResp := func() []byte { return ctrlPkt(MsgCtrlToken, []byte{0xDE, 0xAD, 0xBE, 0xEF}) } // server token at offset 8-11
	accept := func() []byte { return ctrlPkt(MsgCtrlAccept, nil) }
	mapChange := func() []byte {
		msg := packer.PackMsgID(MsgSysMapChange, true)
		msg = append(msg, packer.PackStr("dm1")...)
		msg = append(msg, packer.PackInt(0)...) // crc
		msg = append(msg, packer.PackInt(0)...) // size
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
			ctrlMsg := byte(0xff)
			if ctrl && n > 7 {
				ctrlMsg = buf[7]
			}
			isChunk := !ctrl && hdr.NumChunks > 0
			reply := func(pkt []byte) { _, _ = conn.WriteToUDP(pkt, from) }

			switch phase {
			case 0: // await TOKEN request
				if ctrl && ctrlMsg == MsgCtrlToken {
					if cnt++; cnt > dropN {
						reply(tokenResp())
						phase, cnt = 1, 0
					}
				}
			case 1: // await CONNECT
				if ctrl && ctrlMsg == MsgCtrlConnect {
					if cnt++; cnt > dropN {
						reply(accept())
						phase, cnt = 2, 0
					}
				}
			case 2: // await INFO
				if isChunk {
					if cnt++; cnt > dropN {
						reply(mapChange())
						phase, cnt = 3, 0
					}
				}
			case 3: // await READY
				if isChunk {
					if cnt++; cnt > dropN {
						reply(conReady())
						phase = 4
					}
				}
			}
		}
	}()
	return conn.LocalAddr().String()
}

// V68/B6: Login completes despite the first N packets of every step being
// dropped — the client retransmits the pending step until the server replies.
func TestLoginSurvivesPacketLoss(t *testing.T) {
	addr := lossyMockServer(t, 2)
	s, err := NewSession(addr)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := s.Login(ctx, "tester", ""); err != nil {
		t.Fatalf("Login should survive packet loss via retransmission: %v", err)
	}
	if s.MapName() != "dm1" {
		t.Errorf("map name = %q, want dm1", s.MapName())
	}
}

// Sanity: zero loss completes immediately.
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
