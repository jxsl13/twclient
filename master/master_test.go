package master

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

const fixtureJSON = `{
  "servers": [
    {
      "addresses": ["tw-0.6+udp://1.2.3.4:8303", "tw-0.7+udp://1.2.3.4:8304", "tw-9.9+xx://bad:1"],
      "location": "eu:gb",
      "info": {
        "name": "Test Server",
        "game_type": "DDraceNetwork",
        "map": {"name": "Tutorial", "sha256": "abc", "size": 123},
        "passworded": true,
        "max_clients": 64,
        "max_players": 60,
        "clients": [
          {"name": "alice", "clan": "A", "country": -1, "score": 10, "is_player": true},
          {"name": "bob", "clan": "B", "country": 49, "score": 0, "is_player": false}
        ]
      }
    },
    {
      "addresses": ["tw-0.6+udp://5.6.7.8:8303"],
      "location": "as",
      "info": {"name": "Empty", "game_type": "CTF", "map": {"name": "ctf1"}, "max_clients": 16, "max_players": 16, "clients": null, "unknown_future_field": 42}
    }
  ]
}`

func fixtureServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// V56: list decodes; player list, derived counts, version-tagged addresses,
// unknown-scheme skip, and tolerant decode (unknown field, null clients).
func TestFetchServerListFrom(t *testing.T) {
	srv := fixtureServer(t, http.StatusOK, fixtureJSON)
	entries, err := New().FetchServerListFrom(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	e0 := entries[0]
	// Unknown scheme "tw-9.9+xx" skipped → 2 valid addresses.
	if len(e0.Addresses) != 2 {
		t.Fatalf("addresses = %d, want 2 (unknown scheme skipped)", len(e0.Addresses))
	}
	if e0.Addresses[0].Version != packet.Version06 || e0.Addresses[0].String() != "1.2.3.4:8303" {
		t.Errorf("addr0 = %+v", e0.Addresses[0])
	}
	if e0.Addresses[1].Version != packet.Version07 {
		t.Errorf("addr1 version = %v, want 0.7", e0.Addresses[1].Version)
	}

	in := e0.Info
	if in.Name != "Test Server" || in.GameType != "DDraceNetwork" || in.MapName != "Tutorial" {
		t.Errorf("info basics wrong: %+v", in)
	}
	if !in.Passworded {
		t.Error("passworded should be true")
	}
	if in.NumClients != 2 || in.NumPlayers != 1 {
		t.Errorf("counts: clients=%d players=%d, want 2/1", in.NumClients, in.NumPlayers)
	}
	if in.MaxClients != 64 || in.MaxPlayers != 60 {
		t.Errorf("max: clients=%d players=%d, want 64/60", in.MaxClients, in.MaxPlayers)
	}
	if len(in.Clients) != 2 || in.Clients[0].Name != "alice" || !in.Clients[0].IsPlayer {
		t.Errorf("client list wrong: %+v", in.Clients)
	}

	// Tolerant: unknown field + null clients on entry 2.
	if entries[1].Info.NumClients != 0 || len(entries[1].Info.Clients) != 0 {
		t.Errorf("null clients should yield 0: %+v", entries[1].Info)
	}
}

// V56: failover — first master errors (non-200), second succeeds.
func TestFetchServerListFailover(t *testing.T) {
	bad := fixtureServer(t, http.StatusInternalServerError, "boom")
	good := fixtureServer(t, http.StatusOK, fixtureJSON)
	entries, err := New(WithMasters([]string{bad.URL, good.URL})).FetchServerList(context.Background())
	if err != nil {
		t.Fatalf("failover should reach the good master: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
}

// V56: all masters failing → error.
func TestFetchServerListAllFail(t *testing.T) {
	bad := fixtureServer(t, http.StatusBadGateway, "no")
	if _, err := New(WithMasters([]string{bad.URL})).FetchServerList(context.Background()); err == nil {
		t.Fatal("want error when all masters fail")
	}
}

// V56: context cancellation is honored.
func TestFetchServerListContextCancel(t *testing.T) {
	srv := fixtureServer(t, http.StatusOK, fixtureJSON)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().FetchServerListFrom(ctx, srv.URL); err == nil {
		t.Fatal("want error on cancelled context")
	}
}

// V56: address parser rejects unknown schemes and malformed host:port.
func TestParseAddress(t *testing.T) {
	if a, ok := ParseAddress("tw-0.6+udp://1.2.3.4:8303"); !ok || a.Version != packet.Version06 || a.Port != 8303 {
		t.Errorf("0.6 parse: %+v ok=%v", a, ok)
	}
	if a, ok := ParseAddress("tw-0.7+udp://[::1]:8304"); !ok || a.Version != packet.Version07 || a.Host != "::1" {
		t.Errorf("0.7 ipv6 parse: %+v ok=%v", a, ok)
	}
	for _, bad := range []string{"http://x", "tw-0.6+udp://nohostport", "tw-0.6+udp://h:notaport", "tw-9.9+udp://1.2.3.4:1"} {
		if _, ok := ParseAddress(bad); ok {
			t.Errorf("ParseAddress(%q) should fail", bad)
		}
	}
}
