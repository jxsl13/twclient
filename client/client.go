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
	"sync/atomic"
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

// Client is a headless Teeworlds/DDNet client that wraps a protocol session.
// It handles session creation, login, map download, the background event
// reader, snap state tracking, and input rate-limiting internally.
//
// Users interact with the client through thread-safe accessors for game
// state and action methods (SendInput, SendChat, SendKill).
type Client struct {
	sess Session

	address            string
	name               string
	clan               string
	skin               string
	country            int
	password           string                    // server password, sent in handshake (V42); empty = unprotected
	timeoutCode        string                    // DDNet timeout-code for tee reclaim (V32); stable across reconnect
	rconPassword       string                    // rcon password for auto-login + re-auth (T30/T31); empty = no auto-login
	rconAuthed         bool                      // current rcon auth state (V44), protected by mu
	caps               packet.ServerCapabilities // DDNet server capabilities (V47), protected by mu
	version            packet.Version
	mapCache           *packet.MapCache
	snapStorageSize    int // packet.SnapStorage window for the session reader; 0 = default (V53)
	predInputRingLen   int // prediction input ring size; 0 = default (V54)
	inputTimingRingLen int // CSmoothTime input-timing history ring; 0 = default (V54)
	eventChanSize      int // session reader event-channel buffer; 0 = default (V54)
	readBufferSize     int // UDP receive-buffer size; 0 = default (V54)
	moveEventThreshold int // EventPlayerMove throttle (world units); 0 = default (V127)
	log                *slog.Logger

	// snap state — protected by mu
	mu   sync.RWMutex
	snap SnapStorage

	// in-session player registry (id → name/clan/team/score/present), protected
	// by mu. Fed from EventPlayerJoin/Leave/ScoreChange/TeamSet/SkinChange in
	// handleEvent; cleared on disconnect/reconnect (V98–V105).
	players map[int]PlayerState

	// event processing goroutine — cancelled by Close or parent context
	readerCancel context.CancelFunc
	doneCh       chan struct{}

	// automatic reconnection (T26). connectCtx is the context passed to Connect;
	// cancelling it gracefully stops reconnect retries. closed is shut by Close
	// to abort an in-progress backoff wait; closing marks a deliberate teardown
	// so a server drop during shutdown is not auto-reconnected (V40).
	reconnectPolicy ReconnectPolicy
	connectCtx      context.Context
	closed          chan struct{}
	closeOnce       sync.Once
	closing         atomic.Bool

	// disconnection error — set by event loop, read by Err()
	errMu    sync.Mutex
	lastErr  error
	lastDisc DisconnectReason // classified last CTRL_CLOSE (V34), guarded by errMu

	// prediction time — tracks predicted game ticks for tick-driven input
	predTime PredictedTime

	// server-event callbacks — registered via On/OnXxx, dispatched from the
	// event loop after snap state is updated and mu released (V2, V3)
	callbacks callbackRegistry

	// disconnect callbacks — fired on CTRL_CLOSE with the classified reason
	// (V38), separate from the packet-event registry since the payload is the
	// client-side DisconnectReason
	disconnectMu  sync.Mutex
	disconnectID  uint64
	disconnectCbs map[uint64]func(*Client, DisconnectReason)

	// prediction input history — sent local inputs keyed by predicted tick,
	// re-applied during prediction re-simulation (V9)
	predInputs predInputBuffer

	// prediction config + state (V9, V11) — protected by mu
	predictEnabled bool
	antiping       bool
	predWorld      *PredictedWorld
	prevPredWorld  *PredictedWorld // previous tick's world, for render smoothing (V21)
	predCol        *physics.Collision
	predCfg        physics.WorldConfig // vanilla vs DDRace physics, from map (V10b)
	predTun        physics.Tuning
	tunings        map[int]physics.Tuning // per tune-zone (zone 0 = default), V29

	// cached map view (built lazily once the map is available)
	mapView *MapView

	// events accumulated since the last TickState was built (drained per tick)
	tickEvents []packet.Event

	// pluggable consumers driven per tick (V20, V31): many view-only
	// observers + one view+action controller
	observers  map[uint64]Observer
	obsNextID  uint64
	controller Controller
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

// WithSnapStorageSize sets the retained-snapshot window (packet.SnapStorage
// MaxSnaps) the session reader uses for delta decompression (V53). The default
// (16) is kept when unset or zero; an out-of-range value is clamped by
// packet.WithMaxSnaps. Larger windows tolerate more ack lag at the cost of
// heap; smaller windows trim per-client memory at scale.
func WithSnapStorageSize(n int) Option {
	return func(c *Client) { c.snapStorageSize = n }
}

// WithPredInputRingSize sets the number of recent local inputs retained for
// prediction re-simulation (V54). The default (256) is kept when unset or zero;
// a too-small value is clamped up to a safe floor so the re-sim window stays
// covered. Only relevant when prediction is enabled.
func WithPredInputRingSize(n int) Option {
	return func(c *Client) { c.predInputRingLen = n }
}

// WithInputTimingRingSize sets the number of recent predicted-tick sends kept
// for INPUTTIMING feedback lookup in the smooth predicted clock (V54). This is
// the TW CSmoothTime input-timing history ring (default 200), DISTINCT from
// WithPredInputRingSize (the predicted-world re-sim input history). The default
// is kept when unset or zero; a too-small value is clamped up to a safe floor.
func WithInputTimingRingSize(n int) Option {
	return func(c *Client) { c.inputTimingRingLen = n }
}

// WithEventChanSize sets the buffered capacity of the session reader's event
// channel (V54). The default (128) is kept when unset or zero. A larger buffer
// absorbs bigger event bursts before the event loop drains them.
func WithEventChanSize(n int) Option {
	return func(c *Client) { c.eventChanSize = n }
}

// WithReadBufferSize overrides the UDP socket receive-buffer size (V54). The
// default (2MB) is kept when unset or zero; the OS clamps to its rmem_max. A
// larger buffer reduces snapshot loss under burst load at scale.
func WithReadBufferSize(n int) Option {
	return func(c *Client) { c.readBufferSize = n }
}

// WithMoveEventThreshold sets the minimum Manhattan position delta (world units)
// a visible player must move before an EventPlayerMove fires (V127), throttling
// the otherwise per-tick stream of position updates (V13). The default
// (DefaultMoveEventThreshold, 16) is kept when unset or n <= 0; a larger value
// throttles harder, n=1 emits on any move. Unlike the V54 buffer options this
// default is the library's own throttle, not a value lifted from the original
// client, and it sizes no buffer — it is not a wire constant (V55).
func WithMoveEventThreshold(n int) Option {
	return func(c *Client) { c.moveEventThreshold = n }
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

// WithPassword sets the server password, sent in the NETMSG_INFO handshake
// (V42). Empty means an unprotected server. A wrong or missing password on a
// protected server surfaces as a CTRL_CLOSE with DisconnectKindWrongPassword.
// The password is held on the client and re-sent on every reconnect.
func WithPassword(password string) Option {
	return func(c *Client) { c.password = password }
}

// WithTimeoutCode sets the DDNet timeout code used to reclaim the same tee
// after an unexpected disconnect (V32). The code is stable for the client's
// lifetime and re-sent on every reconnect. If left empty a random stable code
// is generated at construction. Only used on DDNet 0.6 servers that advertise
// the chat-timeout-code capability (V37).
func WithTimeoutCode(code string) Option {
	return func(c *Client) { c.timeoutCode = code }
}

// WithReconnectPolicy sets the automatic reconnection policy (T26). By default
// auto-reconnect is enabled with DefaultReconnectPolicy (exponential backoff,
// unlimited attempts, tee resume). Pass NewReconnectPolicy(...) to customize.
func WithReconnectPolicy(p ReconnectPolicy) Option {
	return func(c *Client) { c.reconnectPolicy = p }
}

// WithoutAutoReconnect disables automatic reconnection; a server drop then
// surfaces via Err()/LastDisconnect without the client retrying.
func WithoutAutoReconnect() Option {
	return func(c *Client) { c.reconnectPolicy.enabled = false }
}

// New creates a new headless client for the given server address.
// By default it uses protocol version 0.6, discards logs, creates its own map
// cache, and auto-reconnects with exponential backoff. Use options to customize.
func New(address string, opts ...Option) *Client {
	c := &Client{
		address:         address,
		name:            packet.DefaultName,
		clan:            "",
		skin:            packet.DefaultSkin,
		country:         packet.DefaultCountry,
		version:         packet.Version06,
		mapCache:        packet.NewMapCache(),
		log:             slog.New(slog.DiscardHandler),
		predTun:         physics.DefaultTuning(),
		predCfg:         physics.DefaultWorldConfig(),
		reconnectPolicy: DefaultReconnectPolicy(),
		closed:          make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil { // a nil option is ignored (V70)
			opt(c)
		}
	}
	// A stable timeout code is required for tee reclaim; generate one when the
	// caller did not provide it (V32).
	if c.timeoutCode == "" {
		c.timeoutCode = generateTimeoutCode()
	}
	// Size the prediction input ring (clamped; default when unset, V54).
	c.predInputs.configure(c.predInputRingLen)
	// Size the input-timing history ring (clamped; default when unset, V54).
	c.predTime.configure(c.inputTimingRingLen)
	// Clamp the move-event throttle to the default when unset (V41/V127).
	if c.moveEventThreshold <= 0 {
		c.moveEventThreshold = DefaultMoveEventThreshold
	}
	return c
}

// Connect creates a new session, performs the protocol handshake, logs in,
// downloads the map, starts the background event reader, and begins
// automatic snap processing. The context governs the entire client lifetime:
// cancelling it stops the background reader and unblocks all I/O.
// After Connect returns, game state is accessible via Character(), RaceTime(), etc.
func (c *Client) Connect(ctx context.Context) (err error) {
	// Remember the caller's context so auto-reconnect can bind to it: cancelling
	// it gracefully aborts reconnect retries (T26, V39).
	c.mu.Lock()
	c.connectCtx = ctx
	c.mu.Unlock()

	sess, err := c.newSession()
	if err != nil {
		return fmt.Errorf("client: dial %s: %w", c.address, err)
	}
	defer func() {
		if err != nil {
			sess.Close()
		}
	}()

	loginOpts := []packet.LoginOption{
		packet.WithLoginSkin(c.skin),
		packet.WithLoginCountry(c.country),
	}
	if c.password != "" {
		loginOpts = append(loginOpts, packet.WithLoginPassword(c.password))
	}
	if err := sess.Login(ctx, c.name, c.clan, loginOpts...); err != nil {
		// A server rejection during login arrives as a CTRL_CLOSE (V109, B10):
		// classify the reason and surface it as ErrServerClosed instead of an
		// opaque timeout, so callers (and auto-reconnect, V34/V35) can react. The
		// session returns the *packet.ServerClosedError from Login; errors.AsType
		// recovers it even if a layer wraps it with %w (V117).
		if sce, ok := errors.AsType[*packet.ServerClosedError](err); ok {
			reason := NewDisconnectReason(sce.Reason)
			c.errMu.Lock()
			c.lastDisc = reason
			c.errMu.Unlock()
			c.setErr(ErrServerClosed)
			return fmt.Errorf("client: login rejected (%s): %w", reason.Kind, ErrServerClosed)
		}
		return fmt.Errorf("client: login: %w", err)
	}

	if _, err := sess.DownloadMap(ctx); err != nil {
		c.log.Warn("map download failed, continuing without map", "error", err)
	}

	// Reset snap state for the new connection. The decoder + is07 select the
	// protocol-neutral snapshot decode path (V112): net7 on 0.7, net6 otherwise;
	// is07 also guards the 0.6-only ObjClientInfo names path (V115).
	is07 := c.version == packet.Version07
	decode := net6.DecodeSnap
	if is07 {
		decode = net7.DecodeSnap
	}
	c.mu.Lock()
	c.snap = SnapStorage{
		lastSnapTime:       time.Now(),
		localCID:           -1,
		decode:             decode,
		is07:               is07,
		moveEventThreshold: c.moveEventThreshold,
	}
	c.players = nil // registry starts empty each (re)connect (V102)
	// Capabilities are sent before MAP_CHANGE and captured synchronously during
	// Login (before the event reader exists), so seed the client copy from the
	// session here; later EventServerCapabilities updates refresh it (V47).
	c.caps = sess.Capabilities()
	c.mu.Unlock()

	c.errMu.Lock()
	c.lastErr = nil
	c.errMu.Unlock()

	c.predTime.Reset()

	// Create a child context for the reader — cancelled by Close or parent ctx.
	readerCtx, readerCancel := context.WithCancel(ctx)
	sess.StartReader(readerCtx)
	c.sess = sess

	// Start background event processing
	c.readerCancel = readerCancel
	c.doneCh = make(chan struct{})
	go c.eventLoop(readerCtx)

	// Register the DDNet timeout code so a later reconnect can reclaim this tee
	// (V32, V37). No-op on non-DDNet servers / 0.7.
	c.sendTimeoutCode()

	// Auto rcon-login when a password is configured, so rcon survives reconnects
	// (T31). Auth state is updated when the server replies (EventRconAuth).
	c.autoRconLogin()

	return nil
}

// Close stops the event processor, disconnects from the server, and
// resets state. Safe to call multiple times. A deliberate Close stops the event
// loop, marks the client as closing so a concurrent server drop is not
// auto-reconnected, aborts any in-progress reconnect backoff, and sends a clean
// CTRL_CLOSE disconnect to the server (V40).
func (c *Client) Close() error {
	c.closing.Store(true)
	c.closeOnce.Do(func() { close(c.closed) })
	return c.closeSession()
}

// closeSession tears down the current session and event loop without marking
// the client closed. Used by both Close and the reconnect path.
func (c *Client) closeSession() error {
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

// Reconnect closes the current session (if any) and establishes a new one,
// re-using the client identity (name/clan/skin/country/password) and the stable
// timeout code (V33). Because Connect re-registers the same /timeout <code>, a
// DDNet 0.6 server reclaims the tee left in the timed-out state, so the player
// resumes the same position/hook/race progress; non-DDNet/0.7 servers ignore
// the code and yield a fresh tee (V32, V37). Call ResetTimeoutCode first to
// force a fresh tee instead of a resume. The new context governs the new
// connection's lifetime.
func (c *Client) Reconnect(ctx context.Context) error {
	c.closeSession()
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

// LastDisconnect returns the classified reason for the most recent CTRL_CLOSE
// (V34), or the zero value if the client has not been disconnected.
func (c *Client) LastDisconnect() DisconnectReason {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.lastDisc
}

// Capabilities returns the DDNet server capabilities last announced for this
// connection, or the zero value if none were sent (V47).
func (c *Client) Capabilities() packet.ServerCapabilities {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.caps
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
// Between tick boundaries, NextInput de-duplicates so at most one input is
// sent per predicted tick.
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
				// Server-initiated drop (channel closed, not ctx cancel):
				// start auto-reconnect unless this is a deliberate teardown.
				c.maybeAutoReconnect()
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
		// Local client id from the session when the protocol carries it outside
		// the snapshot (0.7 Sv_ClientInfo, T140); -1 on 0.6 leaves the in-snapshot
		// Player.Local derivation in updateFromSnap untouched (V115).
		if lid := c.sess.LocalID(); lid >= 0 {
			c.snap.localCID = lid
		}
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
	case packet.EventServerCapabilities:
		c.mu.Lock()
		c.caps = e.Caps
		c.mu.Unlock()
	case packet.EventPlayerJoin:
		c.mu.Lock()
		c.upsertPlayer(e)
		c.mu.Unlock()
	case packet.EventPlayerLeave:
		c.mu.Lock()
		c.removePlayer(e.ClientID)
		c.mu.Unlock()
	case packet.EventScoreChange:
		c.mu.Lock()
		c.setPlayerScore(e.ClientID, e.Score)
		c.mu.Unlock()
	case packet.EventTeamSet:
		c.mu.Lock()
		c.setPlayerTeam(e.ClientID, e.Team)
		c.mu.Unlock()
	case packet.EventSkinChange:
		c.mu.Lock()
		c.setPlayerSkin(e.ClientID, e.Skin)
		c.mu.Unlock()
	case packet.EventRconAuth:
		c.mu.Lock()
		c.rconAuthed = e.Authed
		c.mu.Unlock()
	case packet.EventClose:
		reason := NewDisconnectReason(e.Reason)
		c.errMu.Lock()
		c.lastDisc = reason
		c.errMu.Unlock()
		// rcon auth does not survive a disconnect; clear it so commands are
		// rejected until re-auth (V44). autoRconLogin re-auths on reconnect (V45).
		c.mu.Lock()
		c.rconAuthed = false
		c.clearPlayers() // registry does not survive a disconnect (V102)
		c.mu.Unlock()
		c.log.Warn("server sent CLOSE", "reason", e.Reason, "kind", reason.Kind.String())
		c.setErr(ErrServerClosed)
		c.fireDisconnect(reason)
	}

	// Dispatch to registered callbacks after snap state is updated and any
	// per-case mu has been released, so handlers may safely call back into
	// the client (V2).
	c.callbacks.dispatch(c, ev)

	// Accumulate the event for the next TickState (drained per tick by the
	// tick driver). Snapshot events are excluded — they drive the tick, they
	// are not content of it.
	if _, isSnap := ev.(packet.EventSnapshot); !isSnap {
		c.mu.Lock()
		c.tickEvents = append(c.tickEvents, ev)
		c.mu.Unlock()
	}
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
			net6.WithSnapStorageSize(c.snapStorageSize),
			net6.WithEventChanSize(c.eventChanSize),
			net6.WithReadBufferSize(c.readBufferSize),
		)
	case packet.Version07:
		return net7.NewSession(c.address,
			net7.WithLogger(c.log),
			net7.WithSnapStorageSize(c.snapStorageSize),
			net7.WithEventChanSize(c.eventChanSize),
			net7.WithReadBufferSize(c.readBufferSize),
		)
	default:
		return nil, fmt.Errorf("unsupported protocol version: %d", c.version)
	}
}
