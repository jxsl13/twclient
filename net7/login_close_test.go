package net7

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// closeOnInfoServer drives the 0.7 handshake (TOKEN→token, CONNECT→ACCEPT) then,
// instead of MAP_CHANGE, replies to INFO with a CTRL_CLOSE carrying reason —
// exactly how a teeworlds 0.7 server rejects (wrong password/version, full, ban).
func closeOnInfoServer(t *testing.T, reason string) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("mock listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	ctrlPkt := func(ctrlMsg byte, extra []byte) []byte {
		b := (&Header{Flags: Flags{Control: true}}).Pack()
		b = append(b, ctrlMsg)
		return append(b, extra...)
	}
	go func() {
		buf := make([]byte, 64*1024)
		phase := 0
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
			ctrlMsg := byte(0xff)
			if hdr.Flags.Control && n > 7 {
				ctrlMsg = buf[7]
			}
			switch phase {
			case 0:
				if hdr.Flags.Control && ctrlMsg == MsgCtrlToken {
					reply(ctrlPkt(MsgCtrlToken, []byte{0xDE, 0xAD, 0xBE, 0xEF}))
					phase = 1
				}
			case 1:
				if hdr.Flags.Control && ctrlMsg == MsgCtrlConnect {
					reply(ctrlPkt(MsgCtrlAccept, nil))
					phase = 2
				}
			case 2: // INFO → reject with CTRL_CLOSE
				if !hdr.Flags.Control && hdr.NumChunks > 0 {
					reply(ctrlPkt(MsgCtrlClose, []byte(reason)))
					phase = 3
				}
			}
		}
	}()
	return conn.LocalAddr().String()
}

// V109/B10: a server rejection (CTRL_CLOSE) during login returns ErrServerClosed
// with the reason IMMEDIATELY, not after the context deadline.
func TestLoginFailFastOnClose(t *testing.T) {
	addr := closeOnInfoServer(t, "Wrong password")
	s, err := NewSession(addr)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Generous ctx: the test asserts we return WELL before it (fail-fast).
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	start := time.Now()
	err = s.Login(ctx, "probe", "")
	elapsed := time.Since(start)

	if !errors.Is(err, packet.ErrServerClosed) {
		t.Fatalf("want ErrServerClosed, got %v", err)
	}
	var sce *packet.ServerClosedError
	if !errors.As(err, &sce) || sce.Reason != "Wrong password" {
		t.Fatalf("want reason %q, got %v", "Wrong password", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("login should fail fast on CLOSE, took %v", elapsed)
	}
}
