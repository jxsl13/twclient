// Package client provides a protocol-version-independent headless Teeworlds/DDNet client.
// It creates the appropriate session (0.6 or 0.7) based on configuration and manages
// the full lifecycle: connect → login → map download → event reader → snap processing.
//
// The client automatically processes incoming events (snapshots, race times,
// disconnects) in a background goroutine. Users read game state via thread-safe
// accessors (Character, RaceTime, LastSnapTick, etc.) and interact via
// SendInput, SendChat, and SendKill.
package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
	"github.com/jxsl13/twmap"
)

// compile-time interface checks
var (
	_ Session = (*net6.Session)(nil)
	_ Session = (*net7.Session)(nil)
)

// Sentinel errors returned by Client methods.
var (
	ErrNotConnected    = errors.New("client: not connected")
	ErrServerClosed    = errors.New("client: server sent CLOSE")
	ErrSnapshotTimeout = errors.New("client: snapshot timeout")
)

// inputMinInterval is the minimum interval between INPUT messages when the
// predicted tick has not advanced. This prevents flooding the server with
// duplicate inputs while still allowing periodic re-sends.
const inputMinInterval = 100 * time.Millisecond

// Client is a headless Teeworlds/DDNet client that wraps a protocol session.
// It handles session creation, login, map download, the background event
// reader, snap state tracking, and input rate-limiting internally.
//
// Users interact with the client through thread-safe accessors for game
// state and action methods (SendInput, SendChat, SendKill).
type Client struct {
	sess Session

	address  string
	name     string
	clan     string
	skin     string
	country  int
	version  packet.Version
	mapCache *packet.MapCache
	log      *slog.Logger

	// snap state — protected by mu
	mu   sync.RWMutex
	snap SnapStorage

	// event processing goroutine — cancelled by Close or parent context
	readerCancel context.CancelFunc
	doneCh       chan struct{}

	// disconnection error — set by event loop, read by Err()
	errMu   sync.Mutex
	lastErr error

	// input rate-limiting state — only accessed from SendInput callers
	inputMu       sync.Mutex
	lastInputTime time.Time
	lastInputTick int // last predTick we sent

	// prediction time — tracks predicted game ticks for tick-driven input
	predTime PredictedTime

	// server-event callbacks — registered via On/OnXxx, dispatched from the
	// event loop after snap state is updated and mu released (V2, V3)
	callbacks callbackRegistry

	// prediction input history — sent local inputs keyed by predicted tick,
	// re-applied during prediction re-simulation (V9)
	predInputs predInputBuffer

	// prediction config + state (V9, V11) — protected by mu
	predictEnabled bool
	antiping       bool
	predWorld      *PredictedWorld
	predCol        *physics.Collision
	predTun        physics.Tuning
	tunings        map[int]physics.Tuning // per tune-zone (zone 0 = default), V29

	// cached map view (built lazily once the map is available)
	mapView *MapView
}

// MapView returns a queryable view of the complete local map, or nil if the
// map is not yet available. The view is built once and cached.
func (c *Client) MapView() *MapView {
	c.mu.RLock()
	mv := c.mapView
	c.mu.RUnlock()
	if mv != nil {
		return mv
	}
	m := c.Map()
	if m == nil {
		return nil
	}
	mv = NewMapView(m)
	c.mu.Lock()
	if c.mapView == nil {
		c.mapView = mv
	} else {
		mv = c.mapView
	}
	c.mu.Unlock()
	return mv
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithLogger sets a custom logger. Without this, logging is discarded.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.log = logger
		}
	}
}

// WithVersion sets the protocol version. The default is packet.Version06.
func WithVersion(v packet.Version) Option {
	return func(c *Client) { c.version = v }
}

// WithMapCache sets a shared map cache. Multiple clients using the same
// cache will deduplicate downloads.
func WithMapCache(cache *packet.MapCache) Option {
	return func(c *Client) {
		if cache != nil {
			c.mapCache = cache
		}
	}
}

// WithPrediction enables DDNet-style client-side prediction of the local
// character (V11). When disabled (the default), PredictedCharacter returns the
// raw snapshot state.
func WithPrediction(enabled bool) Option {
	return func(c *Client) { c.predictEnabled = enabled }
}

// WithAntiping enables prediction of other players and entities in addition to
// the local character (full DDNet antiping). Implies prediction is on.
func WithAntiping(enabled bool) Option {
	return func(c *Client) {
		c.antiping = enabled
		if enabled {
			c.predictEnabled = true
		}
	}
}

// WithPlayerInfo sets the player name, clan, skin, and country.
func WithPlayerInfo(name, clan, skin string, country int) Option {
	return func(c *Client) {
		c.name = name
		c.clan = clan
		c.skin = skin
		c.country = country
	}
}

// New creates a new headless client for the given server address.
// By default it uses protocol version 0.6, discards logs, and creates
// its own map cache. Use options to customize.
func New(address string, opts ...Option) *Client {
	c := &Client{
		address:  address,
		name:     "teeworlds",
		clan:     "",
		skin:     "default",
		version:  packet.Version06,
		mapCache: packet.NewMapCache(),
		log:      slog.New(slog.DiscardHandler),
		predTun:  physics.DefaultTuning(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Connect creates a new session, performs the protocol handshake, logs in,
// downloads the map, starts the background event reader, and begins
// automatic snap processing. The context governs the entire client lifetime:
// cancelling it stops the background reader and unblocks all I/O.
// After Connect returns, game state is accessible via Character(), RaceTime(), etc.
func (c *Client) Connect(ctx context.Context) (err error) {
	sess, err := c.newSession()
	if err != nil {
		return fmt.Errorf("client: dial %s: %w", c.address, err)
	}
	defer func() {
		if err != nil {
			sess.Close()
		}
	}()

	if err := sess.Login(ctx, c.name, c.clan, c.skin, c.country); err != nil {
		return fmt.Errorf("client: login: %w", err)
	}

	if _, err := sess.DownloadMap(ctx); err != nil {
		c.log.Warn("map download failed, continuing without map", "error", err)
	}

	// Reset snap state for the new connection
	c.mu.Lock()
	c.snap = SnapStorage{
		lastSnapTime: time.Now(),
		localCID:     -1,
	}
	c.mu.Unlock()

	c.errMu.Lock()
	c.lastErr = nil
	c.errMu.Unlock()

	c.inputMu.Lock()
	c.lastInputTime = time.Time{}
	c.lastInputTick = 0
	c.inputMu.Unlock()

	c.predTime.Reset()

	// Create a child context for the reader — cancelled by Close or parent ctx.
	readerCtx, readerCancel := context.WithCancel(ctx)
	sess.StartReader(readerCtx)
	c.sess = sess

	// Start background event processing
	c.readerCancel = readerCancel
	c.doneCh = make(chan struct{})
	go c.eventLoop(readerCtx)

	return nil
}

// Close stops the event processor, disconnects from the server, and
// resets state. Safe to call multiple times.
func (c *Client) Close() error {
	if c.readerCancel != nil {
		c.readerCancel()
		<-c.doneCh // wait for event loop to finish
		c.readerCancel = nil
		c.doneCh = nil
	}
	if c.sess != nil {
		err := c.sess.Close()
		c.sess = nil
		return err
	}
	return nil
}

// Reconnect closes the current session (if any) and establishes a new one.
// The new context governs the new connection's lifetime.
func (c *Client) Reconnect(ctx context.Context) error {
	c.Close()
	return c.Connect(ctx)
}

// IsConnected returns true if the client has an active session.
func (c *Client) IsConnected() bool { return c.sess != nil }

// Err returns the last error from the background event loop (e.g.
// server disconnect). Returns nil while the connection is healthy.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.lastErr
}

// --- Thread-safe game state accessors ---

// Character returns the last known character state.
func (c *Client) Character() CharacterState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap.character
}

// RaceTime returns the current race time tracking state.
func (c *Client) RaceTime() RaceTime {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap.raceTimeState()
}

// GameInfo returns the last known game info state.
func (c *Client) GameInfo() GameInfoState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap.gameInfo
}

// LastSnapTick returns the tick of the most recent snapshot.
func (c *Client) LastSnapTick() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap.lastTick
}

// LastSnapTime returns the wall-clock time of the most recent snapshot.
func (c *Client) LastSnapTime() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap.lastSnapTime
}

// PredTick returns the current predicted tick from the prediction time tracker.
// Returns 0 if no snapshot has been received yet.
func (c *Client) PredTick() int {
	return c.predTime.PredTick()
}

// AckTick returns the latest acknowledged snapshot tick.
// Returns 0 if no snapshot has been received yet.
func (c *Client) AckTick() int {
	return c.predTime.AckTick()
}

// ResetRace clears the race time state (e.g. between episodes).
func (c *Client) ResetRace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap.raceTime = RaceTime{}
}

// --- Actions ---

// SendInput sends a player input to the server. The client uses the
// PredictedTime tracker to determine the current prediction tick, sending
// input when a new predicted tick boundary is crossed (~50 times/sec).
// Between tick boundaries, inputs are throttled to inputMinInterval.
func (c *Client) SendInput(input packet.PlayerInput) error {
	if c.sess == nil {
		return ErrNotConnected
	}

	// Send exactly one input per predicted-tick boundary (mirrors the real
	// client's "NewPredTick > m_PredTick -> SendInput" gate). Callers may poll
	// this faster than the tick rate; NextInput de-duplicates to one per tick.
	predTick, ackTick, send := c.predTime.NextInput()
	if !send {
		return nil
	}

	c.predInputs.record(predTick, input)
	data := packInput(&input)
	return c.sess.SendInput(ackTick, predTick, inputSize, data)
}

// SendInputForTick sends an input explicitly tagged for the given predicted
// tick, bypassing the per-tick throttle. Replay senders use it to deliver one
// input for EVERY tick even when their polling loop skips a boundary — with a
// few ticks of input lead the server buffers them until their tick.
func (c *Client) SendInputForTick(predTick int, input packet.PlayerInput) error {
	if c.sess == nil {
		return ErrNotConnected
	}
	ackTick := c.predTime.AckTick()
	if ackTick <= 0 || predTick <= 0 {
		return nil
	}
	c.predInputs.record(predTick, input)
	data := packInput(&input)
	return c.sess.SendInput(ackTick, predTick, inputSize, data)
}

// SendChat sends a chat message.
func (c *Client) SendChat(msg string) error {
	if c.sess == nil {
		return ErrNotConnected
	}
	return c.sess.SendChat(msg)
}

// SendKill sends the /kill command.
func (c *Client) SendKill() error {
	if c.sess == nil {
		return ErrNotConnected
	}
	return c.sess.SendKill()
}

// --- Map ---

// Map returns the parsed map or nil.
func (c *Client) Map() *twmap.Map {
	if c.sess == nil {
		return nil
	}
	return c.sess.Map()
}

// MapName returns the current map name.
func (c *Client) MapName() string {
	if c.sess == nil {
		return ""
	}
	return c.sess.MapName()
}

// --- Internal ---

// eventLoop runs in a goroutine and continuously drains events from the
// session, updating snap state. It stops when stopCh is closed or the
// session delivers a close event.
func (c *Client) eventLoop(ctx context.Context) {
	defer close(c.doneCh)

	ch := c.sess.EventCh()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				c.setErr(ErrServerClosed)
				return
			}
			c.handleEvent(ev)
		}
	}
}

func (c *Client) handleEvent(ev packet.Event) {
	switch e := ev.(type) {
	case packet.EventSnapshot:
		var derived []packet.Event
		c.mu.Lock()
		c.snap.processSnapshot(e.Snap)
		if e.Snap != nil {
			derived = c.snap.deriveEvents()
		}
		c.mu.Unlock()
		if e.Snap != nil {
			c.predTime.OnSnapshot(e.Snap.Tick)
			c.reconcilePrediction()
		}
		// Dispatch snap-derived events after releasing mu (V2).
		for _, dev := range derived {
			c.callbacks.dispatch(c, dev)
		}
	case packet.EventRaceFinish:
		c.mu.Lock()
		c.snap.setDDRaceTime(e.TimeCentis, 0, e.Finish)
		c.mu.Unlock()
		if e.Finish {
			c.log.Info("race finished", "time_ms", e.TimeCentis*10)
		}
	case packet.EventCheckpoint:
		c.mu.Lock()
		c.snap.setDDRaceTime(0, e.DiffCentis, false)
		c.mu.Unlock()
	case packet.EventRecord:
		if e.ServerBestCentis > 0 || e.PlayerBestCentis > 0 {
			c.log.Debug("record info",
				"server_best_ms", e.ServerBestCentis*10,
				"player_best_ms", e.PlayerBestCentis*10)
		}
	case packet.EventMapChange:
		c.log.Info("map changed", "map", e.Info.Name)
	case packet.EventInputTiming:
		c.predTime.Adjust(e.IntendedTick, e.TimeLeft)
	case packet.EventTuneParams:
		c.setTuning(e.Raw)
	case packet.EventClose:
		c.log.Warn("server sent CLOSE", "reason", e.Reason)
		c.setErr(ErrServerClosed)
	}

	// Dispatch to registered callbacks after snap state is updated and any
	// per-case mu has been released, so handlers may safely call back into
	// the client (V2).
	c.callbacks.dispatch(c, ev)
}

func (c *Client) setErr(err error) {
	c.errMu.Lock()
	c.lastErr = err
	c.errMu.Unlock()
}

// newSession creates the protocol-specific session based on the configured version.
func (c *Client) newSession() (Session, error) {
	switch c.version {
	case packet.Version06:
		return net6.NewSession(c.address,
			net6.WithLogger(c.log),
			net6.WithMapCache(c.mapCache),
		)
	case packet.Version07:
		return net7.NewSession(c.address,
			net7.WithLogger(c.log),
		)
	default:
		return nil, fmt.Errorf("unsupported protocol version: %d", c.version)
	}
}
