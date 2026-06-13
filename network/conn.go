// Package network provides a UDP connection for the Teeworlds protocol.
package network

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// defaultReadTimeout returns the read timeout from TW_TIMEOUT env or 5s.
func defaultReadTimeout() time.Duration {
	if v := os.Getenv("TW_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 5 * time.Second
}

// defaultReadBufferSize is the UDP socket receive-buffer size used when none is
// configured (V54). 2MB absorbs burst snapshot delivery when many bots share
// scheduling time; the default (786KB) can overflow.
const defaultReadBufferSize = 2 * 1024 * 1024

// Conn wraps a raw UDP connection for sending and receiving teeworlds packets.
// Timeouts are set at construction time via DialOption and are immutable
// afterwards. Use context.WithTimeout for one-off deadline overrides.
type Conn struct {
	conn           *net.UDPConn
	addr           *net.UDPAddr
	readTimeout    time.Duration
	writeTimeout   time.Duration
	readBufferSize int
	log            *slog.Logger
}

// DialOption configures a Conn at construction time.
type DialOption func(*Conn)

// WithReadTimeout overrides the default read timeout.
func WithReadTimeout(d time.Duration) DialOption {
	return func(c *Conn) { c.readTimeout = d }
}

// WithWriteTimeout overrides the default write timeout.
func WithWriteTimeout(d time.Duration) DialOption {
	return func(c *Conn) { c.writeTimeout = d }
}

// WithLogger sets the logger on the connection.
func WithLogger(logger *slog.Logger) DialOption {
	return func(c *Conn) {
		if logger != nil {
			c.log = logger
		}
	}
}

// WithReadBufferSize overrides the UDP socket receive-buffer size (V54). Zero
// or negative keeps the default (2MB); the OS further clamps to its rmem_max.
func WithReadBufferSize(n int) DialOption {
	return func(c *Conn) {
		if n > 0 {
			c.readBufferSize = n
		}
	}
}

// Dial creates a new UDP connection to the given address.
func Dial(address string, opts ...DialOption) (*Conn, error) {
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("dial: resolve %q: %w", address, err)
	}
	udp, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dial: dial %q: %w", address, err)
	}
	rt := defaultReadTimeout()
	c := &Conn{
		conn:           udp,
		addr:           addr,
		readTimeout:    rt,
		writeTimeout:   rt,
		readBufferSize: defaultReadBufferSize,
		log:            slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(c)
	}
	// Enlarge the receive buffer to absorb burst snapshot delivery; the OS
	// default (786KB) can overflow when many bots share scheduling time (V54).
	_ = udp.SetReadBuffer(c.readBufferSize)
	return c, nil
}

// ReadTimeout returns the configured read timeout.
func (c *Conn) ReadTimeout() time.Duration { return c.readTimeout }

// Log returns the connection's logger.
func (c *Conn) Log() *slog.Logger { return c.log }

// Close closes the underlying UDP connection.
func (c *Conn) Close() error {
	return c.conn.Close()
}

// SendRaw sends raw bytes over UDP without any processing.
func (c *Conn) SendRaw(data []byte) error {
	if c.writeTimeout > 0 {
		if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
			return err
		}
	}
	_, err := c.conn.Write(data)
	return err
}

// RecvContext receives a packet, respecting the context's deadline.
// If ctx carries no deadline, the connection's default read timeout is used.
func (c *Conn) RecvContext(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.readTimeout)
	}

	if err := c.conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	buf := make([]byte, packet.MaxPacketSize)
	n, err := c.conn.Read(buf)
	if err != nil {
		// If context was cancelled while we were blocking, surface that.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		return nil, err
	}
	return buf[:n], nil
}
