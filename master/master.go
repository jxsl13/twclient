// Package master fetches the DDNet master server list over HTTPS+JSON and
// queries a single server's info connless (without opening a play session).
// It depends only on the Go standard library (net/http, encoding/json) and is
// read-only: it never creates a game client or mutates connection state (V58).
package master

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// DefaultMasters are the DDNet master servers, tried in order with failover.
// The servers.json list is served over HTTPS at /ddnet/15/servers.json.
var DefaultMasters = []string{
	"https://master1.ddnet.org/ddnet/15/servers.json",
	"https://master2.ddnet.org/ddnet/15/servers.json",
	"https://master3.ddnet.org/ddnet/15/servers.json",
	"https://master4.ddnet.org/ddnet/15/servers.json",
}

// PlayerInfo and ServerInfo are the version-agnostic result types, defined in
// packet (V60) so net6/net7 parsers can return them without an import cycle.
// Aliased here for ergonomic master.ServerInfo/master.PlayerInfo use.
type (
	PlayerInfo = packet.PlayerInfo
	ServerInfo = packet.ServerInfo
)

// Address is one connectable endpoint of a server, with its protocol version.
type Address struct {
	Version packet.Version // Version06 or Version07
	Host    string
	Port    int
}

// String renders the address as host:port (the form network.Dial accepts).
func (a Address) String() string { return net.JoinHostPort(a.Host, strconv.Itoa(a.Port)) }

// ServerEntry is one server in the master list: its endpoints, location, and
// advertised info.
type ServerEntry struct {
	Addresses []Address
	Location  string
	Info      ServerInfo
}

// --- JSON decode (tolerant: unknown fields ignored) ---

type jsonList struct {
	Servers []jsonServer `json:"servers"`
}

type jsonServer struct {
	Addresses []string `json:"addresses"`
	Location  string   `json:"location"`
	Info      jsonInfo `json:"info"`
}

type jsonInfo struct {
	Name       string       `json:"name"`
	GameType   string       `json:"game_type"`
	Map        jsonMap      `json:"map"`
	Passworded bool         `json:"passworded"`
	MaxClients int          `json:"max_clients"`
	MaxPlayers int          `json:"max_players"`
	Clients    []jsonClient `json:"clients"`
}

type jsonMap struct {
	Name string `json:"name"`
}

type jsonClient struct {
	Name     string `json:"name"`
	Clan     string `json:"clan"`
	Country  int    `json:"country"`
	Score    int    `json:"score"`
	IsPlayer bool   `json:"is_player"`
}

// fetchConfig holds FetchServerList options.
type fetchConfig struct {
	client  *http.Client
	masters []string
}

// FetchOption configures FetchServerList / FetchServerListFrom.
type FetchOption func(*fetchConfig)

// WithHTTPClient overrides the HTTP client (e.g. to set a custom timeout or
// proxy). Default: a client with a 10s timeout.
func WithHTTPClient(c *http.Client) FetchOption {
	return func(fc *fetchConfig) {
		if c != nil {
			fc.client = c
		}
	}
}

// WithMasters overrides the master URLs tried by FetchServerList.
func WithMasters(urls []string) FetchOption {
	return func(fc *fetchConfig) {
		if len(urls) > 0 {
			fc.masters = urls
		}
	}
}

func newFetchConfig(opts ...FetchOption) fetchConfig {
	fc := fetchConfig{
		client:  &http.Client{Timeout: 10 * time.Second},
		masters: DefaultMasters,
	}
	for _, opt := range opts {
		opt(&fc)
	}
	return fc
}

// FetchServerList fetches the server list from the first reachable master,
// failing over through the configured masters in order. It returns the entries
// from the first master that responds with decodable JSON; if every master
// fails, it returns the last error (V56).
func FetchServerList(ctx context.Context, opts ...FetchOption) ([]ServerEntry, error) {
	fc := newFetchConfig(opts...)
	var lastErr error
	for _, url := range fc.masters {
		entries, err := fetchFrom(ctx, fc.client, url)
		if err != nil {
			lastErr = err
			continue
		}
		return entries, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("master: no masters configured")
	}
	return nil, fmt.Errorf("master: all masters failed: %w", lastErr)
}

// FetchServerListFrom fetches and decodes the server list from a single URL.
func FetchServerListFrom(ctx context.Context, url string, opts ...FetchOption) ([]ServerEntry, error) {
	fc := newFetchConfig(opts...)
	return fetchFrom(ctx, fc.client, url)
}

func fetchFrom(ctx context.Context, client *http.Client, url string) ([]ServerEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("master: build request %q: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("master: get %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("master: get %q: status %d", url, resp.StatusCode)
	}
	var raw jsonList
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("master: decode %q: %w", url, err)
	}
	return decodeEntries(raw), nil
}

// decodeEntries converts the raw JSON into public ServerEntry values. An
// address with an unknown/unparseable scheme is skipped, never failing the
// whole list (V56).
func decodeEntries(raw jsonList) []ServerEntry {
	entries := make([]ServerEntry, 0, len(raw.Servers))
	for _, s := range raw.Servers {
		var addrs []Address
		for _, a := range s.Addresses {
			if addr, ok := ParseAddress(a); ok {
				addrs = append(addrs, addr)
			}
		}
		entries = append(entries, ServerEntry{
			Addresses: addrs,
			Location:  s.Location,
			Info:      s.Info.toServerInfo(),
		})
	}
	return entries
}

func (ji jsonInfo) toServerInfo() ServerInfo {
	clients := make([]PlayerInfo, 0, len(ji.Clients))
	numPlayers := 0
	for _, c := range ji.Clients {
		if c.IsPlayer {
			numPlayers++
		}
		clients = append(clients, PlayerInfo{
			Name:     c.Name,
			Clan:     c.Clan,
			Country:  c.Country,
			Score:    c.Score,
			IsPlayer: c.IsPlayer,
		})
	}
	return ServerInfo{
		Name:       ji.Name,
		GameType:   ji.GameType,
		MapName:    ji.Map.Name,
		Passworded: ji.Passworded,
		NumPlayers: numPlayers,
		NumClients: len(ji.Clients),
		MaxPlayers: ji.MaxPlayers,
		MaxClients: ji.MaxClients,
		Clients:    clients,
	}
}

// ParseAddress parses a master address URL ("tw-0.6+udp://host:port" /
// "tw-0.7+udp://host:port") into an Address. ok is false for an unknown scheme
// or a malformed host:port (the caller skips it, V56).
func ParseAddress(s string) (Address, bool) {
	var version packet.Version
	switch {
	case strings.HasPrefix(s, "tw-0.6+udp://"):
		version = packet.Version06
		s = strings.TrimPrefix(s, "tw-0.6+udp://")
	case strings.HasPrefix(s, "tw-0.7+udp://"):
		version = packet.Version07
		s = strings.TrimPrefix(s, "tw-0.7+udp://")
	default:
		return Address{}, false // unknown scheme — skip
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return Address{}, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return Address{}, false
	}
	return Address{Version: version, Host: host, Port: port}, true
}
