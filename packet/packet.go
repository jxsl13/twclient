// Package packet provides shared types and utilities for the Teeworlds
// protocol: packet headers, tokens, UDP connections, snapshot storage,
// event plumbing, and chunk-level helpers shared by net6 and net7.
package packet

import (
	"crypto/rand"
	"time"
)

// Token represents a 4-byte security token.
type Token [4]byte

// TokenEmpty is the empty/unknown token (0xFFFFFFFF).
var TokenEmpty = Token{0xFF, 0xFF, 0xFF, 0xFF}

// TokenZero is the zero token.
var TokenZero = Token{0x00, 0x00, 0x00, 0x00}

// Version distinguishes protocol versions.
type Version int

// Protocol versions selected via client.WithVersion / net6 vs net7: Teeworlds
// 0.6 (DDNet variant) and 0.7 (sixup).
const (
	// VersionAuto is the zero value: the client detects the server's protocol
	// at Connect via a connless probe and prefers 0.6 when both answer (V138).
	// Pin a concrete version with client.WithVersion to skip detection.
	VersionAuto Version = 0
	Version06   Version = 6
	Version07   Version = 7
)

// MaxPacketSize is the maximum UDP packet size for teeworlds.
const MaxPacketSize = 1400

// MaxSequence is the sequence number space (wraps at 1024).
const MaxSequence = 1 << 10 // 1024

// SequenceMask masks a sequence number to the valid 10-bit range.
const SequenceMask = MaxSequence - 1 // 1023

// AntiReflectionSize is the number of null bytes sent to prevent reflection attacks.
const AntiReflectionSize = 508

// Connless server-browse magics, shared by 0.6 and 0.7 (SERVERBROWSE_GETINFO /
// SERVERBROWSE_INFO). The connless framing around them differs per protocol
// (net6/net7 BuildInfoRequestConnless), but the magics are identical.
var (
	ServerBrowseGetInfo = []byte{255, 255, 255, 255, 'g', 'i', 'e', '3'}
	ServerBrowseInfo    = []byte{255, 255, 255, 255, 'i', 'n', 'f', '3'}
)

// LoginResendInterval is how long the 0.6/0.7 handshake/login waits for the
// expected reply before retransmitting the pending step (CONNECT/token/INFO/
// READY) via network.Conn.RecvResending. Mirrors DDNet CNetConnection::Update's
// resend timer (V68, V74).
const LoginResendInterval = 500 * time.Millisecond

// TokenRequestDataSize is the fixed payload size for token/connect requests.
const TokenRequestDataSize = 512

// RandomToken generates a cryptographically random 4-byte token.
func RandomToken() Token {
	var t Token
	_, _ = rand.Read(t[:])
	return t
}
