package network_test

import (
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
