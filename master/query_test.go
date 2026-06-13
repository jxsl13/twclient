package master

import (
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// build06Body assembles a 0.6 inf3 body (decimal-string ints) with one client.
func build06Body() []byte {
	var b []byte
	b = append(b, packer.PackStr("12")...)        // token
	b = append(b, packer.PackStr("0.6.5")...)     // version
	b = append(b, packer.PackStr("My Server")...) // name
	b = append(b, packer.PackStr("dm1")...)       // map
	b = append(b, packer.PackStr("DM")...)        // gametype
	b = append(b, packer.PackStr("1")...)         // flags (password)
	b = append(b, packer.PackStr("2")...)         // num players
	b = append(b, packer.PackStr("16")...)        // max players
	b = append(b, packer.PackStr("3")...)         // num clients
	b = append(b, packer.PackStr("16")...)        // max clients
	// client 1 (player)
	b = append(b, packer.PackStr("alice")...)
	b = append(b, packer.PackStr("ACL")...)
	b = append(b, packer.PackStr("-1")...) // country
	b = append(b, packer.PackStr("10")...) // score
	b = append(b, packer.PackStr("1")...)  // is player
	return b
}

// build07Body assembles a 0.7 inf3 body (varint ints) with one client.
func build07Body() []byte {
	var b []byte
	b = append(b, packer.PackInt(99)...)             // token (varint)
	b = append(b, packer.PackStr("0.7")...)          // version
	b = append(b, packer.PackStr("Seven Srv")...)    // name
	b = append(b, packer.PackStr("host.example")...) // hostname (0.7 only)
	b = append(b, packer.PackStr("ctf1")...)         // map
	b = append(b, packer.PackStr("CTF")...)          // gametype
	b = append(b, packer.PackInt(0)...)              // flags (no password)
	b = append(b, packer.PackInt(2)...)              // skill level (0.7 only)
	b = append(b, packer.PackInt(1)...)              // num players
	b = append(b, packer.PackInt(8)...)              // max players
	b = append(b, packer.PackInt(2)...)              // num clients
	b = append(b, packer.PackInt(8)...)              // max clients
	// client 1 (spectator)
	b = append(b, packer.PackStr("bob")...)
	b = append(b, packer.PackStr("")...)
	b = append(b, packer.PackInt(49)...) // country
	b = append(b, packer.PackInt(5)...)  // score
	b = append(b, packer.PackInt(1)...)  // flag: 1 = spectator
	return b
}

// Body decode moved to net6/net7 ParseInfoResponse (V60) — tested there. The
// fake-server round-trip tests below exercise the full QueryServerInfo path,
// including those parsers.

// fakeUDPServer runs handler for each received datagram until the test ends.
// handler returns the bytes to reply (nil = no reply).
func fakeUDPServer(t *testing.T, handler func(req []byte) []byte) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("fake server listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := append([]byte(nil), buf[:n]...)
			if reply := handler(req); reply != nil {
				_, _ = conn.WriteToUDP(reply, from)
			}
		}
	}()
	return conn.LocalAddr().String()
}

// V57/V58: QueryServerInfo (0.6) round-trips against a connless server with no
// login/handshake — only the getinfo exchange.
func TestQueryServerInfo06(t *testing.T) {
	addr := fakeUDPServer(t, func(req []byte) []byte {
		// Expect a connless getinfo; reply with inf3 (6×0xFF + magic + body).
		reply := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
		reply = append(reply, packet.ServerBrowseInfo...)
		reply = append(reply, build06Body()...)
		return reply
	})

	info, err := New(WithQueryTimeout(2*time.Second)).QueryServerInfo(t.Context(), packet.Version06, addr)
	if err != nil {
		t.Fatalf("query 0.6: %v", err)
	}
	if info.Name != "My Server" || len(info.Clients) != 1 {
		t.Errorf("0.6 query result wrong: %+v", info)
	}
}

// V57/V58: QueryServerInfo (0.7) does the token handshake then getinfo, all
// connless, no session.
func TestQueryServerInfo07(t *testing.T) {
	serverToken := []byte{0xA1, 0xB2, 0xC3, 0xD4}
	const ctrlControlFlag = 1 // NET_PACKETFLAG_CONTROL
	addr := fakeUDPServer(t, func(req []byte) []byte {
		// First packet = control token request (flags CONTROL, ctrl byte = MsgCtrlToken).
		if len(req) >= 8 && (req[0]>>2)&ctrlControlFlag != 0 && req[7] == net7.MsgCtrlToken {
			reply := []byte{byte((ctrlControlFlag << 2) & 0xfc), 0, 0}
			reply = append(reply, 0, 0, 0, 0)        // header token (client ignores)
			reply = append(reply, net7.MsgCtrlToken) // ctrl msg id
			reply = append(reply, serverToken...)    // our assigned token (offset 8-11)
			return reply
		}
		// Otherwise = connless getinfo → inf3 reply (9-byte header ignored on recv).
		reply := make([]byte, net7.HeaderSizeConnless)
		reply = append(reply, packet.ServerBrowseInfo...)
		reply = append(reply, build07Body()...)
		return reply
	})

	info, err := New(WithQueryTimeout(2*time.Second)).QueryServerInfo(t.Context(), packet.Version07, addr)
	if err != nil {
		t.Fatalf("query 0.7: %v", err)
	}
	if info.Name != "Seven Srv" || len(info.Clients) != 1 || info.Clients[0].IsPlayer {
		t.Errorf("0.7 query result wrong: %+v", info)
	}
}
