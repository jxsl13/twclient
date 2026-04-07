package client

import (
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
	// Login connects, performs the handshake, and logs in.
	Login(name, clan, skin string, country int) error

	// Close disconnects from the server.
	Close() error

	// StartReader launches the background packet reader.
	// Must be called after Login.
	StartReader()

	// EventCh returns the channel delivering parsed events
	// (snapshots, map changes, disconnects, etc.).
	EventCh() <-chan packet.Event

	// DownloadMap downloads and parses the current map.
	// Uses MapCache if configured.
	DownloadMap() (*twmap.Map, error)

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

	// SendChat sends a chat message.
	SendChat(msg string) error

	// SendKill sends the /kill command.
	SendKill() error
}
