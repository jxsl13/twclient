package client

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/network"
	"github.com/jxsl13/twclient/packet"
)

// DefaultDetectTimeout bounds the connless protocol-detection probe when
// WithDetectTimeout is not set (V138). The probe is ALSO bounded by the Connect
// context, whichever fires first.
const DefaultDetectTimeout = 2 * time.Second

// detectGrace is how long detectVersion keeps listening for a (preferred) 0.6
// reply AFTER a 0.7 info reply already arrived — so a server that speaks BOTH
// still resolves to 0.6 (V139) without making a 0.7-only server pay the full
// detect window. Never extends past the original deadline.
const detectGrace = 250 * time.Millisecond

// ErrVersionDetectFailed is returned by Connect when protocol auto-detect is
// active (unpinned version) and NEITHER 0.6 nor 0.7 answered the connless probe
// within the detect window (V139). Pin the protocol with WithVersion to skip
// detection entirely.
var ErrVersionDetectFailed = errors.New("client: protocol auto-detect failed: no connless reply from server (pin WithVersion to skip detection)")

// detectVersion probes the target server addr DIRECTLY over the connless
// protocol — the same getinfo the in-game server browser / LAN scan send to a
// server, NOT a master-list lookup, so it works for unregistered/LAN/private
// servers with no master entry (V138). It opens ONE UDP socket, sends a 0.6
// GETINFO and a 0.7 token request, then:
//   - a 0.6 info reply  → Version06 immediately (0.6 is preferred, V139);
//   - a 0.7 token reply → send the token-routed 0.7 getinfo;
//   - a 0.7 info reply  → Version07, but keep listening a short grace for a
//     late 0.6 reply so a both-protocol server still resolves to 0.6 (V139).
//
// All wire bytes come from net6/net7 helpers (no hand-rolled bytes, V59/V60).
// Bounded by timeout AND ctx (V66). Returns ErrVersionDetectFailed if neither
// protocol answers in time.
func detectVersion(ctx context.Context, addr string, timeout time.Duration) (packet.Version, error) {
	if timeout <= 0 {
		timeout = DefaultDetectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := network.Dial(addr)
	if err != nil {
		return packet.VersionAuto, fmt.Errorf("client: detect dial %s: %w", addr, err)
	}
	defer conn.Close()

	clientToken := packet.RandomToken()
	if err := conn.SendRaw(net6.BuildInfoRequestConnless(byte(rand.IntN(256)))); err != nil {
		return packet.VersionAuto, fmt.Errorf("client: detect send 0.6 getinfo: %w", err)
	}
	if err := conn.SendRaw(net7.BuildTokenRequest(clientToken)); err != nil {
		return packet.VersionAuto, fmt.Errorf("client: detect send 0.7 token request: %w", err)
	}

	saw07 := false
	for {
		data, err := conn.RecvContext(ctx)
		if err != nil {
			// Any recv failure ENDS the probe. If 0.7 answered earlier, commit to
			// it; otherwise the window elapsed with no usable reply — report the
			// typed detect failure. The underlying err takes several forms (ctx
			// deadline/cancel, or a socket i/o-timeout derived from the deadline),
			// so don't branch on it — wrap it as the cause so errors.Is still
			// matches ErrVersionDetectFailed (V139).
			if saw07 {
				return packet.Version07, nil
			}
			return packet.VersionAuto, fmt.Errorf("%w (%v)", ErrVersionDetectFailed, err)
		}

		if _, ok := net6.ConnlessInfoPayload(data); ok {
			return packet.Version06, nil // preferred — return the moment 0.6 answers
		}
		if tok, ok := net7.ParseTokenResponse(data); ok {
			gi := net7.BuildInfoRequestConnless(tok, clientToken, int(rand.Int32()))
			if err := conn.SendRaw(gi); err != nil {
				return packet.VersionAuto, fmt.Errorf("client: detect send 0.7 getinfo: %w", err)
			}
			continue
		}
		if _, ok := net7.ConnlessInfoPayload(data); ok {
			if !saw07 {
				saw07 = true
				// Give a late 0.6 reply a brief grace, then commit to 0.7. The
				// child deadline never extends past the original window.
				gctx, gcancel := context.WithTimeout(ctx, detectGrace)
				defer gcancel()
				ctx = gctx
			}
			continue
		}
	}
}
