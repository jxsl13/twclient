//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/packet"
)

// B28/V146: ddnet-small serves dm1, a converted/minimal map that lacks the
// optional MAPITEMTYPE_INFO item. It must still download AND parse — twclient
// parses leniently (twmap.WithRequireInfo(false)). WithRequireMap makes a parse
// failure surface as a Connect error, so this is a hard regression: before the
// fix, Connect failed with "map download failed: parse map: missing map info item".
func TestLiveInfolessMapParses(t *testing.T) {
	requireHarness(t)
	addr := env("TW_E2E_DDNET_SMALL", "ddnet-small:8303")
	c := client.New(addr,
		client.WithVersion(packet.Version06),
		client.WithoutAutoReconnect(),
		client.WithRequireMap())
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect ddnet-small (dm1, info-less): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if !c.HasMap() {
		t.Fatal("HasMap()=false — info-less dm1 did not parse (V146)")
	}
	t.Log("ddnet-small dm1 (info-less) downloaded + parsed: HasMap=true")
}
