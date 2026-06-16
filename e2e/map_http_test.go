//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/packet"
)

// mapsDDNetReachable probes the public DDNet map store. It is EXTERNAL infra we
// don't run, so the HTTP map-download test SKIPS when it's unreachable (V140/V148).
func mapsDDNetReachable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, client.DefaultMapDownloadURL+"/", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// V147/V148: a 0.6 connect with HTTP map download enabled obtains the map — the
// client builds <base>/<name>_<sha256hex>.map from the live MAP_CHANGE sha and
// fetches it over HTTP(S) (falling back to UDP if the map isn't on the store).
// Smoke test against REAL maps.ddnet.org; SKIPS if that infra is unreachable.
func TestLiveHTTPMapDownload(t *testing.T) {
	requireHarness(t)
	if !mapsDDNetReachable() {
		t.Skip("maps.ddnet.org unreachable — external infra we don't run (V140/V148)")
	}
	addr := env("TW_E2E_DDNET_06", "ddnet:8303")
	c := client.New(addr,
		client.WithVersion(packet.Version06),
		client.WithoutAutoReconnect(),
		client.WithRequireMap(),
		client.WithMapDownloadURL(client.DefaultMapDownloadURL))
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect with HTTP map download: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if !c.HasMap() {
		t.Fatal("HasMap()=false with HTTP map download enabled")
	}
	t.Logf("connected with WithMapDownloadURL set; HasMap=true (HTTP, or UDP fallback)")
}
