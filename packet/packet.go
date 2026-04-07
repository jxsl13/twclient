// Package packet provides shared types and utilities for the Teeworlds
// protocol: packet headers, tokens, UDP connections, snapshot storage,
// event plumbing, and chunk-level helpers shared by net6 and net7.
package packet

import (
	"crypto/rand"
)

// Token represents a 4-byte security token.
type Token [4]byte

// TokenEmpty is the empty/unknown token (0xFFFFFFFF).
var TokenEmpty = Token{0xFF, 0xFF, 0xFF, 0xFF}

// TokenZero is the zero token.
var TokenZero = Token{0x00, 0x00, 0x00, 0x00}

// Version distinguishes protocol versions.
type Version int

const (
	Version06 Version = 6
	Version07 Version = 7
)

// MaxPacketSize is the maximum UDP packet size for teeworlds.
const MaxPacketSize = 1400

// MaxSequence is the sequence number space (wraps at 1024).
const MaxSequence = 1 << 10 // 1024

// SequenceMask masks a sequence number to the valid 10-bit range.
const SequenceMask = MaxSequence - 1 // 1023

// AntiReflectionSize is the number of null bytes sent to prevent reflection attacks.
const AntiReflectionSize = 508

// TokenRequestDataSize is the fixed payload size for token/connect requests.
const TokenRequestDataSize = 512

// RandomToken generates a cryptographically random 4-byte token.
func RandomToken() Token {
	var t Token
	_, _ = rand.Read(t[:])
	return t
}
