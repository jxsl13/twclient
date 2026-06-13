package master

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/network"
	"github.com/jxsl13/twclient/packet"
)

// Connless server-info query: ask a single server for its info over the
// connless protocol, without opening a play session (V57/V58). Both wire
// framing AND body decode come from net6/net7 helpers — this package never
// touches packet bytes or fields (V59/V60); it only orchestrates the UDP
// exchange and returns the packet.ServerInfo the parser produced.
const (
	maxInfoReads = 8 // stray-packet tolerance while waiting for the info reply
)

// queryConfig holds QueryServerInfo options.
type queryConfig struct {
	timeout time.Duration
}

// QueryOption configures QueryServerInfo.
type QueryOption func(*queryConfig)

// WithQueryTimeout sets the overall timeout for the query (default 5s).
func WithQueryTimeout(d time.Duration) QueryOption {
	return func(qc *queryConfig) {
		if d > 0 {
			qc.timeout = d
		}
	}
}

// QueryServerInfo asks the server at addr for its current info over the
// connless protocol, without logging in (V57). version selects the wire format
// (packet.Version06 / packet.Version07). It opens only a UDP socket — no
// Handshake, Login, or session — and returns the parsed ServerInfo (incl. the
// current Clients player list). The call is bounded by the option timeout and
// the context.
func QueryServerInfo(ctx context.Context, version packet.Version, addr string, opts ...QueryOption) (ServerInfo, error) {
	qc := queryConfig{timeout: 5 * time.Second}
	for _, o := range opts {
		o(&qc)
	}
	ctx, cancel := context.WithTimeout(ctx, qc.timeout)
	defer cancel()

	conn, err := network.Dial(addr)
	if err != nil {
		return ServerInfo{}, fmt.Errorf("master: dial %s: %w", addr, err)
	}
	defer conn.Close()

	switch version {
	case packet.Version06:
		return query6(ctx, conn)
	case packet.Version07:
		return query7(ctx, conn)
	default:
		return ServerInfo{}, fmt.Errorf("master: unsupported version %v", version)
	}
}

// --- 0.6 ---

func query6(ctx context.Context, conn *network.Conn) (ServerInfo, error) {
	token := byte(rand.IntN(256))
	if err := conn.SendRaw(net6.BuildInfoRequestConnless(token)); err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.6 send getinfo: %w", err)
	}
	for range maxInfoReads {
		data, err := conn.RecvContext(ctx)
		if err != nil {
			return ServerInfo{}, fmt.Errorf("master: 0.6 recv info: %w", err)
		}
		if body, ok := net6.ConnlessInfoPayload(data); ok {
			return net6.ParseInfoResponse(body)
		}
	}
	return ServerInfo{}, fmt.Errorf("master: 0.6 no info response")
}

// --- 0.7 ---

func query7(ctx context.Context, conn *network.Conn) (ServerInfo, error) {
	clientToken := packet.RandomToken()

	// 1. Token handshake (reuse net7's tested builder + framing): the server
	// replies with its token, which routes our connless getinfo to it.
	if err := conn.SendRaw(net7.BuildTokenRequest(clientToken)); err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.7 send token request: %w", err)
	}
	serverToken, err := recvServerToken7(ctx, conn)
	if err != nil {
		return ServerInfo{}, err
	}

	// 2. Connless getinfo, framed by net7's connless helper.
	gi := net7.BuildInfoRequestConnless(serverToken, clientToken, int(rand.Int32()))
	if err := conn.SendRaw(gi); err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.7 send getinfo: %w", err)
	}
	for range maxInfoReads {
		data, err := conn.RecvContext(ctx)
		if err != nil {
			return ServerInfo{}, fmt.Errorf("master: 0.7 recv info: %w", err)
		}
		if body, ok := net7.ConnlessInfoPayload(data); ok {
			return net7.ParseInfoResponse(body)
		}
	}
	return ServerInfo{}, fmt.Errorf("master: 0.7 no info response")
}

// recvServerToken7 waits for the server's NET_CTRLMSG_TOKEN control reply and
// returns the token it assigned, via net7.ParseTokenResponse.
func recvServerToken7(ctx context.Context, conn *network.Conn) (packet.Token, error) {
	for range maxInfoReads {
		data, err := conn.RecvContext(ctx)
		if err != nil {
			return packet.Token{}, fmt.Errorf("master: 0.7 recv token: %w", err)
		}
		if tok, ok := net7.ParseTokenResponse(data); ok {
			return tok, nil
		}
	}
	return packet.Token{}, fmt.Errorf("master: 0.7 no token reply")
}
