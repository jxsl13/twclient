package client

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packet"
)

const detectCtrlFlag = 1 // NET_PACKETFLAG_CONTROL

// fakeDetectServer runs a connless responder that answers the 0.6 GETINFO and/or
// the 0.7 token+GETINFO exchange, gated by reply06/reply07. It lets
// detectVersion's protocol classification + 0.6-preference be unit-tested
// against a loopback socket (the real-server path is e2e/TestLiveAutoDetect,
// V119). Returns the server host:port. detectVersion only checks that
// ConnlessInfoPayload accepts a reply, so the info bodies can be empty.
func fakeDetectServer(t *testing.T, reply06, reply07 bool) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("fake detect server listen: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	serverToken := []byte{0xA1, 0xB2, 0xC3, 0xD4}
	info06 := append([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, packet.ServerBrowseInfo...)
	info07 := append(make([]byte, net7.HeaderSizeConnless), packet.ServerBrowseInfo...)
	tokenReply := append([]byte{byte((detectCtrlFlag << 2) & 0xfc), 0, 0, 0, 0, 0, 0, net7.MsgCtrlToken}, serverToken...)

	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := buf[:n]
			switch {
			case len(req) >= 8 && (req[0]>>2)&detectCtrlFlag != 0 && req[7] == net7.MsgCtrlToken:
				if reply07 { // 0.7 control token request → assign a token
					_, _ = conn.WriteToUDP(tokenReply, from)
				}
			case bytes.HasPrefix(req, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}):
				if reply06 { // 0.6 connless getinfo
					_, _ = conn.WriteToUDP(info06, from)
				}
			default: // 0.7 token-routed connless getinfo
				if reply07 {
					_, _ = conn.WriteToUDP(info07, from)
				}
			}
		}
	}()
	return conn.LocalAddr().String()
}

// V138/V139: detectVersion classifies the server's protocol from a direct
// connless probe, prefers 0.6 when both answer, and errors when neither does.
func TestDetectVersion(t *testing.T) {
	cases := []struct {
		name             string
		reply06, reply07 bool
		want             packet.Version
		wantErr          bool
	}{
		{"both → prefer 0.6", true, true, packet.Version06, false},
		{"only 0.6", true, false, packet.Version06, false},
		{"only 0.7", false, true, packet.Version07, false},
		{"neither → error", false, false, packet.VersionAuto, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := fakeDetectServer(t, tc.reply06, tc.reply07)
			v, err := detectVersion(t.Context(), addr, time.Second)
			if tc.wantErr {
				if !errors.Is(err, ErrVersionDetectFailed) {
					t.Fatalf("err = %v, want ErrVersionDetectFailed", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("detectVersion: %v", err)
			}
			if v != tc.want {
				t.Errorf("version = %v, want %v", v, tc.want)
			}
		})
	}
}

// detectVersion honors a cancelled context promptly (V66) rather than blocking
// the whole window on a silent server.
func TestDetectVersionCtxCancel(t *testing.T) {
	addr := fakeDetectServer(t, false, false) // silent — never replies
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := detectVersion(ctx, addr, 5*time.Second); !errors.Is(err, ErrVersionDetectFailed) {
		t.Fatalf("err = %v, want ErrVersionDetectFailed on cancelled ctx", err)
	}
}

// V138/V139: WithVersion pins the protocol (Version() reports it immediately);
// unset leaves VersionAuto until a successful auto-detect Connect.
func TestVersionAccessorPin(t *testing.T) {
	if v := New("x:8303").Version(); v != packet.VersionAuto {
		t.Errorf("unpinned Version() = %v, want VersionAuto", v)
	}
	if v := New("x:8303", WithVersion(packet.Version07)).Version(); v != packet.Version07 {
		t.Errorf("pinned Version() = %v, want Version07", v)
	}
}
