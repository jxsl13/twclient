package client_test

import (
	"context"
	"fmt"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/packet"
)

// Construct a headless client, register a chat callback, then connect. New
// applies sane defaults (0.6, auto-reconnect, prediction off); options override
// them. Connect blocks through handshake+login (see DDNet CClient connect flow).
func ExampleClient() {
	c := client.New("localhost:8303",
		client.WithPlayerInfo("nameless tee", "", "default", -1),
		client.WithVersion(packet.Version06),
	)
	c.OnChat(func(_ *client.Client, e packet.EventChat) {
		fmt.Printf("[chat] %s\n", e.Msg)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		return // no server in this example
	}
	defer c.Close()
	_ = c.SendChat("hello")
}

// Options are plain values you can build up before New.
func ExampleWithPrediction() {
	c := client.New("localhost:8303",
		client.WithPrediction(true),    // predict the local tee
		client.WithSnapStorageSize(32), // deeper delta window
		client.WithReconnectPolicy(client.NewReconnectPolicy()),
	)
	_ = c
	fmt.Println("configured")
	// Output: configured
}
