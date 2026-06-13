package client

import (
	"crypto/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// ResetTimeoutCode regenerates the client's timeout code. The next reconnect
// then registers a NEW code, so a DDNet server will NOT reclaim the previously
// timed-out tee and the player gets a fresh tee instead. Call this before
// Reconnect when a clean session is wanted rather than a resume (V32, V37).
func (c *Client) ResetTimeoutCode() {
	c.mu.Lock()
	c.timeoutCode = generateTimeoutCode()
	c.mu.Unlock()
}

// timeoutCodeAlphabet is the character set for generated timeout codes.
const timeoutCodeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// generateTimeoutCode returns a random 16-character timeout code. DDNet derives
// its code from a seed + server address, but the server only compares the code
// for equality on reclaim, so any stable per-client value works (V32).
func generateTimeoutCode() string {
	const n = 16
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "twclientfallback"
	}
	for i := range b {
		b[i] = timeoutCodeAlphabet[int(b[i])%len(timeoutCodeAlphabet)]
	}
	return string(b)
}

// TimeoutCode returns the DDNet timeout code this client registers for tee
// reclaim (V32). It is stable across reconnects.
func (c *Client) TimeoutCode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.timeoutCode
}

// sendTimeoutCode registers the timeout code with the server via the DDNet chat
// command "/timeout <code>", so a later reconnect can reclaim this tee (V32,
// V37). It is a no-op unless the server is DDNet 0.6 and advertises the
// chat-timeout-code capability.
func (c *Client) sendTimeoutCode() {
	c.mu.RLock()
	code := c.timeoutCode
	caps := c.caps
	sess := c.sess
	c.mu.RUnlock()

	if sess == nil || code == "" || c.version != packet.Version06 || !caps.ChatTimeoutCode {
		return
	}
	if err := sess.SendChat("/timeout " + code); err != nil {
		c.log.Warn("failed to send timeout code", "error", err)
	}
}

// DisconnectKind classifies why a server connection ended, derived from the
// CTRL_CLOSE reason string (V34).
type DisconnectKind int

const (
	// DisconnectKindClosed is a plain close with no specific reason.
	DisconnectKindClosed DisconnectKind = iota
	// DisconnectKindKicked is an explicit kick (not a ban).
	DisconnectKindKicked
	// DisconnectKindBanned is a ban; BanDuration holds the remaining time when
	// the server states it, otherwise 0 (unknown/permanent).
	DisconnectKindBanned
	// DisconnectKindTimedOut is a connection timeout reported by the server.
	DisconnectKindTimedOut
	// DisconnectKindShuttingDown is a server shutdown/restart.
	DisconnectKindShuttingDown
	// DisconnectKindFull is a rejection because the server is full.
	DisconnectKindFull
	// DisconnectKindWrongPassword is a rejection for a wrong/missing password.
	DisconnectKindWrongPassword
	// DisconnectKindUnknown is any other reason; Text holds the raw string.
	DisconnectKindUnknown
)

// String returns a short name for the kind.
func (k DisconnectKind) String() string {
	switch k {
	case DisconnectKindClosed:
		return "closed"
	case DisconnectKindKicked:
		return "kicked"
	case DisconnectKindBanned:
		return "banned"
	case DisconnectKindTimedOut:
		return "timed_out"
	case DisconnectKindShuttingDown:
		return "shutting_down"
	case DisconnectKindFull:
		return "full"
	case DisconnectKindWrongPassword:
		return "wrong_password"
	default:
		return "unknown"
	}
}

// DisconnectReason is the classified result of a CTRL_CLOSE (V34). Text is the
// verbatim server reason; BanDuration is the parsed remaining ban time when the
// kind is Banned and the server reported a finite duration (0 otherwise).
type DisconnectReason struct {
	Kind        DisconnectKind
	Text        string
	BanDuration time.Duration
}

// banMinutesRe matches DDNet's ban message duration, e.g.
// "You have been banned for 5 minutes (reason)" or "... for 1 minute (...)".
var banMinutesRe = regexp.MustCompile(`for (\d+) minute`)

// NewDisconnectReason classifies a raw CTRL_CLOSE reason string into a
// DisconnectReason (V34, V41). The raw text is always preserved verbatim.
func NewDisconnectReason(raw string) DisconnectReason {
	r := DisconnectReason{Text: raw}
	low := strings.ToLower(strings.TrimSpace(raw))

	switch {
	case low == "":
		r.Kind = DisconnectKindClosed
	case strings.Contains(low, "wrong password") || strings.Contains(low, "no password"):
		r.Kind = DisconnectKindWrongPassword
	case strings.Contains(low, "banned"):
		r.Kind = DisconnectKindBanned
		if m := banMinutesRe.FindStringSubmatch(low); m != nil {
			if mins, err := strconv.Atoi(m[1]); err == nil {
				r.BanDuration = time.Duration(mins) * time.Minute
			}
		}
	case strings.Contains(low, "kicked"):
		r.Kind = DisconnectKindKicked
	case strings.Contains(low, "shutdown") || strings.Contains(low, "shutting down") || strings.Contains(low, "restart"):
		r.Kind = DisconnectKindShuttingDown
	case strings.Contains(low, "full"):
		r.Kind = DisconnectKindFull
	case strings.Contains(low, "timeout") || strings.Contains(low, "timed out"):
		r.Kind = DisconnectKindTimedOut
	default:
		r.Kind = DisconnectKindUnknown
	}
	return r
}
