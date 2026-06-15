package master

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"time"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packet"
)

// LAN discovery: broadcast the connless GETINFO over UDP to the local network
// and collect the servers that answer (V135). Like QueryServerInfo it never
// hand-rolls wire bytes — net6/net7 build the requests and parse the replies
// (V59/V60); this only orchestrates the broadcast + per-source collection.

// DefaultScanPorts is the Teeworlds LAN port range probed by ScanLAN when
// WithScanPorts is not given.
const (
	DefaultScanPortLo = 8303
	DefaultScanPortHi = 8310
)

// DefaultScanTimeout is how long ScanLAN listens for replies when
// WithScanTimeout is not given.
const DefaultScanTimeout = 2 * time.Second

// LANServer is one server discovered on the local network.
type LANServer struct {
	Addr    string         // "host:port" of the responding server
	Version packet.Version // Version06 or Version07
	Info    ServerInfo     // the parsed server info (incl. the player list)
}

type scanConfig struct {
	portLo, portHi int
	bcastAddrs     []string
	timeout        time.Duration
}

// ScanOption configures a ScanLAN call.
type ScanOption func(*scanConfig)

// WithScanPorts overrides the UDP port range probed (default 8303–8310).
// Out-of-range or reversed values are ignored.
func WithScanPorts(lo, hi int) ScanOption {
	return func(s *scanConfig) {
		if lo > 0 && hi >= lo && hi < 65536 {
			s.portLo, s.portHi = lo, hi
		}
	}
}

// WithBroadcastAddrs overrides the broadcast target IPs (default
// {"255.255.255.255"}). A targeted unicast IP also works (e.g. for tests).
func WithBroadcastAddrs(addrs []string) ScanOption {
	return func(s *scanConfig) {
		if len(addrs) > 0 {
			s.bcastAddrs = addrs
		}
	}
}

// WithScanTimeout sets how long ScanLAN listens for replies (default
// DefaultScanTimeout). The context deadline still applies.
func WithScanTimeout(d time.Duration) ScanOption {
	return func(s *scanConfig) {
		if d > 0 {
			s.timeout = d
		}
	}
}

// ScanLAN discovers servers on the local network by broadcasting the connless
// GETINFO (both protocols) to the configured broadcast addresses and port range,
// then collecting and parsing the replies until the scan timeout or ctx (V135).
// Best-effort: it returns the servers gathered so far when the window elapses
// (a missed reply is not an error). 0.6 answers directly; 0.7 first returns a
// token, to which ScanLAN sends a token-routed getinfo. Results are deduped by
// (address, version).
func (c *Client) ScanLAN(ctx context.Context, opts ...ScanOption) ([]LANServer, error) {
	cfg := scanConfig{
		portLo:     DefaultScanPortLo,
		portHi:     DefaultScanPortHi,
		bcastAddrs: []string{"255.255.255.255"},
		timeout:    DefaultScanTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("master: lan scan listen: %w", err)
	}
	defer conn.Close()
	enableBroadcast(conn) // best-effort: needed for a real 255.255.255.255 target

	// Unblock the read loop promptly on ctx done (timeout or cancel), V66.
	go func() {
		<-ctx.Done()
		_ = conn.SetReadDeadline(time.Now())
	}()

	clientToken := packet.RandomToken()

	// Broadcast: 0.6 getinfo + 0.7 token request to every addr × port.
	for _, a := range cfg.bcastAddrs {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		for p := cfg.portLo; p <= cfg.portHi; p++ {
			dst := &net.UDPAddr{IP: ip, Port: p}
			_, _ = conn.WriteToUDP(net6.BuildInfoRequestConnless(byte(rand.IntN(256))), dst)
			_, _ = conn.WriteToUDP(net7.BuildTokenRequest(clientToken), dst)
		}
	}

	found := map[string]LANServer{}
	key := func(addr string, v packet.Version) string { return addr + "|" + strconv.Itoa(int(v)) }
	buf := make([]byte, 4096)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // read deadline (ctx done) or socket closed → done
		}
		data := buf[:n]
		addr := src.String()

		if body, ok := net6.ConnlessInfoPayload(data); ok {
			if info, e := net6.ParseInfoResponse(body); e == nil {
				found[key(addr, packet.Version06)] = LANServer{Addr: addr, Version: packet.Version06, Info: info}
			}
			continue
		}
		if body, ok := net7.ConnlessInfoPayload(data); ok {
			if info, e := net7.ParseInfoResponse(body); e == nil {
				found[key(addr, packet.Version07)] = LANServer{Addr: addr, Version: packet.Version07, Info: info}
			}
			continue
		}
		// 0.7 token reply → route a getinfo back to this specific server.
		if tok, ok := net7.ParseTokenResponse(data); ok {
			gi := net7.BuildInfoRequestConnless(tok, clientToken, int(rand.Int32()))
			_, _ = conn.WriteToUDP(gi, src)
		}
	}

	out := make([]LANServer, 0, len(found))
	for _, s := range found {
		out = append(out, s)
	}
	return out, nil
}

// enableBroadcast sets SO_BROADCAST on the socket so WriteToUDP to a broadcast
// address is permitted. Best-effort: a unicast scan (or a target that doesn't
// need it) works regardless. The setsockopt fd type differs per OS, so the call
// lives in build-tagged setBroadcast (broadcast_unix.go / broadcast_windows.go).
func enableBroadcast(conn *net.UDPConn) {
	rc, err := conn.SyscallConn()
	if err != nil {
		return
	}
	_ = rc.Control(func(fd uintptr) { _ = setBroadcast(fd) })
}
