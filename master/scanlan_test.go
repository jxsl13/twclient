package master

import (
	"net"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// fake06Server replies to any datagram with a canned 0.6 connless INFO response
// (6×0xFF + inf3 magic + decimal-string body), like a real server answering
// GETINFO. Returns its port.
func fake06Server(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	var body []byte
	for _, f := range []string{"12", "0.6.5", "LAN Server", "dm1", "DM", "0", "1", "16", "1", "16", "bob", "", "0", "5", "1"} {
		body = append(body, packer.PackString(f)...)
	}
	resp := append(append([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, packet.ServerBrowseInfo...), body...)

	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_ = n
			_, _ = conn.WriteToUDP(resp, from)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// V135: ScanLAN discovers a 0.6 server via the connless broadcast path, parses
// its info, and dedupes (the server answers both our 0.6 + 0.7 probes).
func TestScanLANDiscovers06(t *testing.T) {
	port := fake06Server(t)
	c := New()

	servers, err := c.ScanLAN(t.Context(),
		WithBroadcastAddrs([]string{"127.0.0.1"}),
		WithScanPorts(port, port),
		WithScanTimeout(1500*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("ScanLAN: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("found %d servers, want 1 (deduped): %+v", len(servers), servers)
	}
	s := servers[0]
	if s.Version != packet.Version06 {
		t.Errorf("version = %v, want 0.6", s.Version)
	}
	if s.Info.Name != "LAN Server" || s.Info.MapName != "dm1" {
		t.Errorf("info wrong: %+v", s.Info)
	}
	if _, _, err := net.SplitHostPort(s.Addr); err != nil {
		t.Errorf("addr %q not host:port: %v", s.Addr, err)
	}
}

// No server listening → empty result, no error (best-effort, returns on timeout).
func TestScanLANEmpty(t *testing.T) {
	c := New()
	servers, err := c.ScanLAN(t.Context(),
		WithBroadcastAddrs([]string{"127.0.0.1"}),
		WithScanPorts(8999, 8999),
		WithScanTimeout(300*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("ScanLAN: %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("found %d servers, want 0: %+v", len(servers), servers)
	}
}

// Option guards: bad port range / empty addrs / non-positive timeout are ignored.
func TestScanOptionGuards(t *testing.T) {
	cfg := scanConfig{portLo: 1, portHi: 2, bcastAddrs: []string{"x"}, timeout: time.Second}
	WithScanPorts(99, 1)(&cfg)    // hi < lo → ignored
	WithBroadcastAddrs(nil)(&cfg) // empty → ignored
	WithScanTimeout(-1)(&cfg)     // non-positive → ignored
	if cfg.portLo != 1 || cfg.portHi != 2 || len(cfg.bcastAddrs) != 1 || cfg.timeout != time.Second {
		t.Errorf("guards failed: %+v", cfg)
	}
	WithScanPorts(8303, 8310)(&cfg)
	if cfg.portLo != 8303 || cfg.portHi != 8310 {
		t.Errorf("valid ports not applied: %+v", cfg)
	}
}
