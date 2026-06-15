// Package udpfault is a TEST-ONLY in-process UDP fault-injection proxy (SPEC
// V125). It sits between a client and a target UDP server and applies a seeded,
// per-direction fault policy (drop / duplicate / latency) to real datagrams, so
// tests can drive the real network.Conn + net6/net7 transport through controlled
// packet loss — deterministically, with no external tool (toxiproxy is TCP-only)
// and no root (unlike tc/netem). stdlib net only.
//
// One client is assumed (the tests dial the proxy once); the client address is
// learned from its first datagram. Faults are reproducible for a fixed Policy.Seed.
package udpfault

import (
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Policy configures the per-direction fault injection. Rates are probabilities
// in [0,1]; zero values = a transparent pass-through. C2S = client→server,
// S2C = server→client.
type Policy struct {
	DropC2S, DropS2C float64       // probability a datagram is dropped
	DupC2S, DupS2C   float64       // probability a datagram is duplicated (sent twice)
	LatencyC2S       time.Duration // fixed forwarding delay client→server
	LatencyS2C       time.Duration // fixed forwarding delay server→client
	Seed             int64         // RNG seed → reproducible drop/dup pattern
}

// Proxy forwards UDP between a client (dialing Addr) and the target server,
// injecting faults per Policy. Create with New, stop with Close.
type Proxy struct {
	down   *net.UDPConn // client-facing socket (client dials this)
	up     *net.UDPConn // upstream socket (proxy ↔ target)
	policy Policy

	mu         sync.Mutex
	clientAddr *net.UDPAddr // learned from the first client datagram
	c2s        *rand.Rand   // used only by the down→up goroutine
	s2c        *rand.Rand   // used only by the up→down goroutine

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup

	dropped atomic.Uint64 // datagrams dropped (both relay goroutines touch it)
}

// New starts a proxy forwarding to target (host:port) under policy. The client
// should send to proxy.Addr().
func New(target string, policy Policy) (*Proxy, error) {
	taddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	down, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	up, err := net.DialUDP("udp", nil, taddr)
	if err != nil {
		down.Close()
		return nil, err
	}
	p := &Proxy{
		down:   down,
		up:     up,
		policy: policy,
		c2s:    rand.New(rand.NewSource(policy.Seed)),
		s2c:    rand.New(rand.NewSource(policy.Seed + 1)),
		done:   make(chan struct{}),
	}
	p.wg.Go(p.downToUp)
	p.wg.Go(p.upToDown)
	return p, nil
}

// Addr is the client-facing address; clients dial this instead of the server.
func (p *Proxy) Addr() string { return p.down.LocalAddr().String() }

// Close stops the proxy and waits for its goroutines to exit (no leak).
func (p *Proxy) Close() error {
	p.closeOnce.Do(func() {
		close(p.done)
		p.down.Close()
		p.up.Close()
	})
	p.wg.Wait()
	return nil
}

// downToUp relays client→server datagrams, learning the client address.
func (p *Proxy) downToUp() {
	buf := make([]byte, 65535)
	for {
		n, caddr, err := p.down.ReadFromUDP(buf)
		if err != nil {
			return // socket closed
		}
		p.mu.Lock()
		p.clientAddr = caddr
		p.mu.Unlock()
		p.forward(p.c2s, p.policy.DropC2S, p.policy.DupC2S, p.policy.LatencyC2S, buf[:n], func(b []byte) {
			_, _ = p.up.Write(b)
		})
	}
}

// upToDown relays server→client datagrams to the learned client address.
func (p *Proxy) upToDown() {
	buf := make([]byte, 65535)
	for {
		n, err := p.up.Read(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		caddr := p.clientAddr
		p.mu.Unlock()
		if caddr == nil {
			continue // no client seen yet
		}
		p.forward(p.s2c, p.policy.DropS2C, p.policy.DupS2C, p.policy.LatencyS2C, buf[:n], func(b []byte) {
			_, _ = p.down.WriteToUDP(b, caddr)
		})
	}
}

// forward applies drop/dup/latency for one datagram and sends it via send. The
// payload is copied so send may run after the read buffer is reused.
func (p *Proxy) forward(rng *rand.Rand, drop, dup float64, latency time.Duration, data []byte, send func([]byte)) {
	if drop > 0 && rng.Float64() < drop {
		p.dropped.Add(1)
		return
	}
	cp := append([]byte(nil), data...)
	dupit := dup > 0 && rng.Float64() < dup
	deliver := func() {
		send(cp)
		if dupit {
			send(cp)
		}
	}
	if latency > 0 {
		// Deliver after the delay without blocking the read loop, so a later
		// datagram can overtake (reorder). Bounded by Close via done.
		p.wg.Go(func() {
			t := time.NewTimer(latency)
			defer t.Stop()
			select {
			case <-t.C:
				deliver()
			case <-p.done:
			}
		})
		return
	}
	deliver()
}

// Dropped returns the number of datagrams dropped.
func (p *Proxy) Dropped() uint64 { return p.dropped.Load() }
