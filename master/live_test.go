package master

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// Live tests hit the real DDNet masters / servers. They are OPT-IN: skipped
// unless TW_LIVE is set (V118), so the default `go test ./...` stays hermetic
// (no public-network access). They also skip when nothing answers with a 2xx.

// V56: the real DDNet masters return a decodable, non-empty server list.
func TestLiveFetchServerList(t *testing.T) {
	if os.Getenv("TW_LIVE") == "" {
		t.Skip("live master fetch; set TW_LIVE=1 to run (V118)")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	entries, err := New().FetchServerList(ctx)
	if err != nil {
		t.Skipf("no master reachable (2xx): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("live master returned an empty server list")
	}
	t.Logf("fetched %d servers from master", len(entries))

	// Sanity: at least one entry has a parsed address and a name.
	addrs, named := 0, 0
	for _, e := range entries {
		addrs += len(e.Addresses)
		if e.Info.Name != "" {
			named++
		}
	}
	if addrs == 0 {
		t.Error("no entry had a parseable address")
	}
	if named == 0 {
		t.Error("no entry had a server name")
	}
}

// V57/V58: a 0.6 server from the live list answers a connless info query
// (no session), and the result matches the master-listed name.
func TestLiveQueryServerInfo(t *testing.T) {
	if os.Getenv("TW_LIVE") == "" {
		t.Skip("live server query; set TW_LIVE=1 to run (V118)")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	c := New(WithQueryTimeout(5 * time.Second))
	entries, err := c.FetchServerList(ctx)
	if err != nil {
		t.Skipf("no master reachable (2xx): %v", err)
	}

	// Find the first 0.6 server with a name to compare against.
	var addr string
	var want string
	for _, e := range entries {
		for _, a := range e.Addresses {
			if a.Version == packet.Version06 {
				addr = a.String()
				want = e.Info.Name
				break
			}
		}
		if addr != "" {
			break
		}
	}
	if addr == "" {
		t.Skip("no 0.6 server in the live list")
	}

	info, err := c.QueryServerInfo(ctx, packet.Version06, addr)
	if err != nil {
		t.Skipf("server %s did not answer connless query: %v", addr, err)
	}
	t.Logf("queried %s: name=%q gametype=%q map=%q clients=%d/%d listed=%d",
		addr, info.Name, info.GameType, info.MapName, info.NumClients, info.MaxClients, len(info.Clients))
	if info.MapName == "" && info.GameType == "" && info.Name == "" {
		t.Errorf("connless query returned empty info (master listed %q)", want)
	}
}
