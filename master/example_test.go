package master_test

import (
	"context"
	"fmt"

	"github.com/jxsl13/twclient/master"
	"github.com/jxsl13/twclient/packet"
)

// Fetch the DDNet master server list. New applies defaults (the four DDNet
// masters, ChooseFastest policy replicating DDNet's CChooseMaster); methods are
// on the Client. (Skips quietly here if no master is reachable.)
func ExampleClient_FetchServerList() {
	c := master.New()
	entries, err := c.FetchServerList(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("fetched %d servers\n", len(entries))
}

// Parse a master address URL into a version-tagged endpoint.
func ExampleParseAddress() {
	addr, ok := master.ParseAddress("tw-0.6+udp://127.0.0.1:8303")
	fmt.Println(ok, addr.Version == packet.Version06, addr.String())
	// Output: true true 127.0.0.1:8303
}
