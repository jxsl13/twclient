package network_test

import (
	"context"
	"fmt"
	"time"

	"github.com/jxsl13/twclient/network"
)

// Dial a Teeworlds server address with a custom read timeout and receive
// buffer. network.Conn is a thin UDP transport — it moves raw bytes and knows
// nothing about the protocol; net6/net7 sit on top. (UDP "dial" only fixes the
// peer address; no packet is sent here.)
func ExampleDial() {
	conn, err := network.Dial("127.0.0.1:8303",
		network.WithReadTimeout(2*time.Second),
		network.WithReadBufferSize(4*1024*1024),
	)
	if err != nil {
		fmt.Println("dial error:", err)
		return
	}
	defer conn.Close()
	fmt.Println("read timeout:", conn.ReadTimeout())
	// Output: read timeout: 2s
}

// Send a raw datagram and wait for a reply bounded by a context — the
// request/response primitive the net6/net7 handshakes build on. RecvContext
// returns as soon as a packet arrives or the context is done, so a cancellable
// timeout caps the wait without a fixed read deadline. (Compile-only: it needs a
// live peer to actually exchange bytes.)
func ExampleConn_requestResponse() {
	conn, err := network.Dial("127.0.0.1:8303", network.WithReadTimeout(2*time.Second))
	if err != nil {
		return
	}
	defer conn.Close()

	if err := conn.SendRaw([]byte{0x10, 0x00}); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := conn.RecvContext(ctx)
	if err != nil {
		return
	}
	fmt.Printf("server replied with %d bytes\n", len(resp))
}
