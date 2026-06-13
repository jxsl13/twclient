package packet

// Server-info result types, shared by the master-list JSON decode and the
// connless info query (parsed in net6/net7). Version-agnostic foundation types
// (V60): net6/net7 return these without importing a consumer package.

// ServerInfoFlagPassword marks a password-protected server in the connless info
// flags field (SERVER_FLAG_PASSWORD / SERVERINFO_FLAG_PASSWORD).
const ServerInfoFlagPassword = 1

// PlayerInfo is one current client (player or spectator) on a server.
type PlayerInfo struct {
	Name     string
	Clan     string
	Country  int
	Score    int
	IsPlayer bool
}

// ServerInfo is a server's advertised state. Returned both from the master
// list (JSON) and from a connless QueryServerInfo, so callers handle one shape.
type ServerInfo struct {
	Name       string
	GameType   string
	MapName    string
	Passworded bool
	NumPlayers int // clients with IsPlayer
	NumClients int // all clients (players + spectators)
	MaxPlayers int
	MaxClients int
	Clients    []PlayerInfo // current player/spectator list
}
