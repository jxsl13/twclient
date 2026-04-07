// Package network provides a UDP connection for the Teeworlds protocol.
package network

import (
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

// Conn wraps a raw UDP connection for sending and receiving teeworlds packets.
// Timeouts are set at construction time via DialOption and are immutable
// afterwards. Use RecvWithTimeout for one-off deadline overrides.
type Conn struct {
	conn         *net.UDPConn
	addr         *net.UDPAddr
	readTimeout  time.Duration
	writeTimeout time.Duration
	log          *slog.Logger
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
		conn:         udp,
		addr:         addr,
		readTimeout:  rt,
		writeTimeout: rt,
		log:          slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(c)
	}
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

// Recv receives a packet and returns the data slice.
// It uses the connection's configured read timeout.
func (c *Conn) Recv() ([]byte, error) {
	return c.recvWith(c.readTimeout)
}

// RecvWithTimeout receives a packet using the given deadline override.
// Pass 0 for no timeout.
func (c *Conn) RecvWithTimeout(timeout time.Duration) ([]byte, error) {
	return c.recvWith(timeout)
}

func (c *Conn) recvWith(timeout time.Duration) ([]byte, error) {
	if timeout > 0 {
		if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return nil, err
		}
	}
	buf := make([]byte, packet.MaxPacketSize)
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}
