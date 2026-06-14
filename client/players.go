package client

import (
	"iter"

	"github.com/jxsl13/twclient/packet"
)

// PlayerState is the in-session registry record for one player: identity and
// scoreboard fields gathered from ClientInfo/PlayerInfo across 0.6 and 0.7
// (V100). It is the protocol-unified shape — a consumer never branches on the
// protocol version to read a name, clan, team or score.
//
// On 0.6 the name/clan/country/skin come from the ObjClientInfo snapshot object
// (DDNet packs them as ints, decoded via net6.IntsToStr); Score and Team arrive
// only via change events, so they read 0 until the first EventScoreChange /
// EventTeamSet for that player. On 0.7 every field is fed from the
// Sv_ClientInfo / Sv_Team / score messages.
type PlayerState struct {
	ClientID int
	Name     string
	Clan     string
	Country  int
	Skin     string
	Team     int // game team / spectator
	Score    int
	Local    bool // ClientID == LocalID
	Present  bool // currently connected
}

// PlayerView pairs a player's registry record with its live character from the
// latest snapshot, so a scoreboard+render consumer reads name/clan/score AND
// position in a single pass. HasCharacter is false when the player is connected
// but has no character in the latest snapshot (spectator, not yet spawned).
type PlayerView struct {
	PlayerState
	Character    CharacterState
	HasCharacter bool
}

// Player returns the registry record for a client id, or ok=false if absent.
// Safe to call from any goroutine (V101).
func (c *Client) Player(id int) (PlayerState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.players[id]
	return p, ok
}

// Players returns a single-pass iterator over every registered player, merging
// each player's stored registry info with its live character (O(1) lookup)
// under ONE read lock — so a scoreboard+render consumer iterates once, not once
// per map (V104). Iterate with `for p := range c.Players()`.
//
// The yielded PlayerView must not be retained beyond the loop body, and the
// loop body must not call back into the Client (it runs under the read lock).
// The iterator allocates nothing per element.
func (c *Client) Players() iter.Seq[PlayerView] {
	return func(yield func(PlayerView) bool) {
		c.mu.RLock()
		defer c.mu.RUnlock()
		for id, ps := range c.players {
			ch, ok := c.snap.characters[id]
			if !yield(PlayerView{PlayerState: ps, Character: ch, HasCharacter: ok}) {
				return
			}
		}
	}
}

// Roster returns a copy of every registry record (unsorted). Sort by Score or
// Team to render a scoreboard. The returned slice shares no state with the
// registry and is safe to retain.
func (c *Client) Roster() []PlayerState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]PlayerState, 0, len(c.players))
	for _, p := range c.players {
		out = append(out, p)
	}
	return out
}

// --- registry mutation (callers hold c.mu) ---

// upsertPlayer applies a join: insert or refresh identity fields, preserving any
// existing Score/Team (a join carries identity, not score). It marks the player
// present and sets Local when the id matches the local client (V103, V105).
func (c *Client) upsertPlayer(e packet.EventPlayerJoin) {
	if c.players == nil {
		c.players = make(map[int]PlayerState)
	}
	p := c.players[e.ClientID] // zero value if new; preserves Score/Team on refresh
	p.ClientID = e.ClientID
	p.Name = e.Name
	p.Clan = e.Clan
	p.Country = e.Country
	p.Skin = e.Skin
	p.Team = e.Team
	p.Local = e.ClientID == c.snap.localCID
	p.Present = true
	c.players[e.ClientID] = p
}

// removePlayer drops a player on leave so no stale name lingers (V99).
func (c *Client) removePlayer(id int) {
	delete(c.players, id)
}

// setPlayerScore updates only Score, never touching Name/Clan/Team (V105). A
// score for an unknown id creates a minimal present entry (e.g. a 0.6 score
// event before the ClientInfo object is seen).
func (c *Client) setPlayerScore(id, score int) {
	if c.players == nil {
		c.players = make(map[int]PlayerState)
	}
	p, ok := c.players[id]
	if !ok {
		p = PlayerState{ClientID: id, Present: true, Local: id == c.snap.localCID}
	}
	p.Score = score
	c.players[id] = p
}

// setPlayerTeam updates only Team, never touching Name/Clan/Score (V105).
func (c *Client) setPlayerTeam(id, team int) {
	if c.players == nil {
		c.players = make(map[int]PlayerState)
	}
	p, ok := c.players[id]
	if !ok {
		p = PlayerState{ClientID: id, Present: true, Local: id == c.snap.localCID}
	}
	p.Team = team
	c.players[id] = p
}

// setPlayerSkin updates only Skin (0.7 Sv_SkinChange), never other fields (V105).
func (c *Client) setPlayerSkin(id int, skin string) {
	if c.players == nil {
		return
	}
	if p, ok := c.players[id]; ok {
		p.Skin = skin
		c.players[id] = p
	}
}

// clearPlayers empties the registry on disconnect / before reconnect so ghosts
// never carry across sessions (V102).
func (c *Client) clearPlayers() { c.players = nil }
