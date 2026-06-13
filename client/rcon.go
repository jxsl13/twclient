package client

import (
	"context"
	"errors"
	"time"
)

// ErrNotAuthed is returned by Rcon when the client has not completed rcon
// authentication (V44).
var ErrNotAuthed = errors.New("client: not rcon-authenticated")

// WithRconPassword sets the remote-console password. When set, the client
// auto-authenticates after connect and re-authenticates on every reconnect
// (T31). The password is never logged in cleartext.
func WithRconPassword(password string) Option {
	return func(c *Client) { c.rconPassword = password }
}

// RconAuthed reports whether the client is currently rcon-authenticated (V44).
func (c *Client) RconAuthed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rconAuthed
}

// RconLogin authenticates with the remote console using password and blocks
// until the server confirms (EventRconAuth) or ctx is done. The password is
// stored for re-auth on reconnect.
func (c *Client) RconLogin(ctx context.Context, password string) error {
	c.mu.RLock()
	sess := c.sess
	c.mu.RUnlock()
	if sess == nil {
		return ErrNotConnected
	}

	c.mu.Lock()
	c.rconPassword = password
	c.rconAuthed = false
	c.mu.Unlock()

	if err := sess.SendRconAuth(password); err != nil {
		return err
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if c.RconAuthed() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Rcon sends a remote-console command. It fails with ErrNotAuthed when the
// client is not authenticated, and ErrNotConnected when there is no session
// (V44).
func (c *Client) Rcon(cmd string) error {
	c.mu.RLock()
	sess := c.sess
	authed := c.rconAuthed
	c.mu.RUnlock()
	if sess == nil {
		return ErrNotConnected
	}
	if !authed {
		return ErrNotAuthed
	}
	return sess.SendRconCmd(cmd)
}

// autoRconLogin sends an rcon auth request when a password is configured. The
// auth state is set asynchronously when the server replies (EventRconAuth).
func (c *Client) autoRconLogin() {
	c.mu.RLock()
	sess := c.sess
	pw := c.rconPassword
	c.mu.RUnlock()
	if sess == nil || pw == "" {
		return
	}
	if err := sess.SendRconAuth(pw); err != nil {
		c.log.Warn("rcon auto-login failed", "error", err)
	}
}
