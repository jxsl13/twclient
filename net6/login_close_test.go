package net6

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// closeOnInfoServer drives the 0.6 handshake (CONNECT→CONNECTACCEPT) then, instead
// of MAP_CHANGE, replies to INFO with a CTRL_CLOSE carrying reason — how a server
// rejects (wrong password, full, ban).
func closeOnInfoServer(t *testing.T, reason string) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("mock listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	connAccept := func() []byte {
		return BuildCtrlPacketNoToken(0, append([]byte{MsgCtrlConnectAccept}, 0xDE, 0xAD, 0xBE, 0xEF))
	}
	closePkt := func() []byte {
		return BuildCtrlPacketNoToken(0, append([]byte{MsgCtrlClose}, []byte(reason)...))
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
			ctrl := hdr.Flags.Control
			reply := func(pkt []byte) { _, _ = conn.WriteToUDP(pkt, from) }
			switch phase {
			case 0:
				if ctrl && n > hdr.Size() && buf[hdr.Size()] == MsgCtrlConnect {
					reply(connAccept())
					phase = 1
				}
			case 1: // INFO → reject with CTRL_CLOSE
				if !ctrl && hdr.NumChunks > 0 {
					reply(closePkt())
					phase = 2
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
