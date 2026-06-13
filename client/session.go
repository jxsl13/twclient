package client

import (
	"context"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
)

// Session is a protocol-version-independent interface to a Teeworlds server.
// The session handles handshake, login, keepalive, ack tracking, snap delta
// decompression, and multi-part snap assembly internally.
//
// Callers interact with the session through typed events (snapshots, map
// changes, disconnects) rather than raw packet bytes.
type Session interface {
	// Login connects, performs the handshake, and logs in. Only name and clan
	// are required; skin, country, and password are optional variadic options
	// (packet.WithLoginSkin/WithLoginCountry/WithLoginPassword) that fall back
	// to the DDNet/Teeworlds defaults when omitted.
	// The context controls the handshake and login timeout.
	Login(ctx context.Context, name, clan string, opts ...packet.LoginOption) error

	// Close disconnects from the server.
	Close() error

	// StartReader launches the background packet reader.
	// The context governs the reader's lifetime.
	StartReader(ctx context.Context)

	// EventCh returns the channel delivering parsed events
	// (snapshots, map changes, disconnects, etc.).
	EventCh() <-chan packet.Event

	// DownloadMap downloads and parses the current map.
	// Uses MapCache if configured.
	DownloadMap(ctx context.Context) (*twmap.Map, error)

	// Map returns the parsed map or nil.
	Map() *twmap.Map

	// MapName returns the current map name.
	MapName() string

	// GetMapInfo returns the current map metadata.
	GetMapInfo() packet.MapInfo

	// SetMap replaces the parsed map.
	SetMap(m *twmap.Map, info packet.MapInfo)

	// Poll returns the next event, blocking until one is available or
	// the context is cancelled. Returns nil after Close.
	Poll() (packet.Event, error)

	// SendInput sends a player input message for the given tick.
	SendInput(tick, predTick, inputSize int, inputData []byte) error

	// SendChat sends a public chat message.
	SendChat(msg string) error

	// SendChatTeam sends a chat message; team selects team vs all chat.
	SendChatTeam(team bool, msg string) error

	// SendWhisper sends a private message to a client (protocol-unified).
	SendWhisper(toID int, msg string) error

	// SendKill sends the /kill command.
	SendKill() error

	// SendEmoticon shows an emoticon above the player.
	SendEmoticon(e packet.Emoticon) error

	// SendSetTeam requests a team change.
	SendSetTeam(team int) error

	// SendSpectate sets the spectated client (or free-view).
	SendSpectate(spectatorID int) error

	// SendVote casts a yes/no vote on the running vote.
	SendVote(approve bool) error

	// SendCallVote starts a vote.
	SendCallVote(voteType, value, reason string) error
}
