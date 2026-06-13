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

// DefaultHTTPTimeout is the timeout of the HTTP client FetchServerList builds
// when WithHTTPClient is not given (V62).
const DefaultHTTPTimeout = 10 * time.Second

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

// Client fetches the master server list and queries individual servers. Build
// it with New; all request entry points are methods on it (V64). It is safe for
// concurrent use (the RequestPolicy may carry shared state, e.g. RoundRobin's
// cursor or ChooseFastest's cached best master).
type Client struct {
	masters      []string
	http         *http.Client
	policy       RequestPolicy
	queryTimeout time.Duration
}

// Option configures a Client at construction.
type Option func(*Client)

// WithMasters overrides the master URLs (default DefaultMasters).
func WithMasters(urls []string) Option {
	return func(c *Client) {
		if len(urls) > 0 {
			c.masters = urls
		}
	}
}

// WithHTTPClient overrides the HTTP client (default: timeout DefaultHTTPTimeout).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.http = hc
		}
	}
}

// WithRequestPolicy sets how FetchServerList selects among masters (default
// ChooseFastest, replicating DDNet — see RequestPolicy).
func WithRequestPolicy(p RequestPolicy) Option {
	return func(c *Client) {
		if p != nil {
			c.policy = p
		}
	}
}

// WithQueryTimeout sets the per-call timeout for QueryServerInfo (default
// DefaultQueryTimeout).
func WithQueryTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.queryTimeout = d
		}
	}
}

// New builds a Client with the given options, applying defaults for any unset.
func New(opts ...Option) *Client {
	c := &Client{
		masters:      DefaultMasters,
		http:         &http.Client{Timeout: DefaultHTTPTimeout},
		policy:       ChooseFastest(),
		queryTimeout: DefaultQueryTimeout,
	}
	for _, opt := range opts {
		if opt != nil { // a nil option is ignored (V70)
			opt(c)
		}
	}
	return c
}

// FetchServerList fetches the server list, letting the RequestPolicy pick which
// master(s) to hit (default ChooseFastest). Returns the first valid list; if
// every master fails, the last error (V56/V64).
func (c *Client) FetchServerList(ctx context.Context) ([]ServerEntry, error) {
	return c.policy.Fetch(ctx, c.masters, c.fetchFrom)
}

// FetchServerListFrom fetches and decodes the list from a single explicit
// master URL, bypassing the policy.
func (c *Client) FetchServerListFrom(ctx context.Context, url string) ([]ServerEntry, error) {
	return c.fetchFrom(ctx, url)
}

func (c *Client) fetchFrom(ctx context.Context, url string) ([]ServerEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("master: build request %q: %w", url, err)
	}
	resp, err := c.http.Do(req)
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
