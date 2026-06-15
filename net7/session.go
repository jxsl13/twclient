package net7

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jxsl13/twclient/network"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
	"github.com/teeworlds-go/huffman/v2"
)

// Option configures a Session at construction time.
type Option func(*options)

// options holds the user-configurable settings collected from Option funcs
// before the Session is built. It is deliberately separate from Session so
// options apply once to a small value rather than twice to a half-built
// Session (the logger's "proto" tag, sizes, and cache are resolved here).
type options struct {
	log             *slog.Logger
	mapCache        *packet.MapCache
	snapStorageSize int
	eventChanSize   int
	readBufferSize  int
}

// WithLogger sets a custom logger for the session.
// Without this option, logging is silently discarded.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.log = logger
		}
	}
}

// WithMapCache sets a shared map cache. Multiple sessions using the same
// cache will deduplicate downloads: only the first session to request a
// map actually downloads it; the rest wait and reuse the cached result.
func WithMapCache(cache *packet.MapCache) Option {
	return func(o *options) {
		if cache != nil {
			o.mapCache = cache
		}
	}
}

// WithSnapStorageSize sets the retained-snapshot window (packet.SnapStorage
// MaxSnaps) used by the reader for delta decompression (V53). Zero or unset
// keeps the default; the value is validated by packet.WithMaxSnaps.
func WithSnapStorageSize(n int) Option {
	return func(o *options) { o.snapStorageSize = n }
}

// WithEventChanSize sets the buffered capacity of the reader's event channel
// (V54). Zero or unset keeps the default (128); a positive value is used as-is.
func WithEventChanSize(n int) Option {
	return func(o *options) { o.eventChanSize = n }
}

// WithReadBufferSize overrides the UDP receive-buffer size (V54). Zero or unset
// keeps the default (2MB); forwarded to network.Dial.
func WithReadBufferSize(n int) Option {
	return func(o *options) { o.readBufferSize = n }
}

// Session tracks the connection state for a 0.7 client session.
type Session struct {
	conn        *network.Conn
	clientToken packet.Token
	serverToken packet.Token
	ack         int
	sequence    int
	log         *slog.Logger

	mu            sync.Mutex // protects ack and sequence for concurrent read/write
	lastAckedSnap atomic.Int64
	// localClientID is the local player's client id. 0.7 has no Local bit in the
	// snapshot PlayerInfo (V115); the server marks the local player's Sv_ClientInfo
	// with m_Local=true (teeworlds gameclient.cpp:827), captured in
	// processClientInfo. -1 until learned (T140).
	localClientID atomic.Int64
	capsMu        sync.RWMutex
	caps          packet.ServerCapabilities // DDNet caps over sixup (B20/V124)
	mapMu         sync.RWMutex
	mapInfo       packet.MapInfo
	parsed        *twmap.Map
	loginMapData  []byte           // raw map bytes drained during login (B11/V110); consumed by DownloadMap
	mapCache      *packet.MapCache // always set: shared or per-session
	reader        reader           // background reader state (activated by StartReader)

	snapStorageSize int // configured packet.SnapStorage window; 0 = default (V53)
	eventChanSize   int // configured reader event-channel buffer; 0 = default (V54)
	readBufferSize  int // configured UDP receive-buffer size; 0 = default (V54)

	huff    *huffman.Huffman // shared decompressor (precomputed dict, read-only)
	huffBuf []byte           // synchronous-path Decompress buffer; reader uses its own (V75)
}

// NewSession creates a new 0.7 session against the given address.
// By default, logging is discarded. Use WithLogger to customize.
func NewSession(address string, opts ...Option) (*Session, error) {
	o := options{
		log:      slog.New(slog.DiscardHandler),
		mapCache: packet.NewMapCache(), // per-session default; WithMapCache overrides
	}
	for _, opt := range opts {
		if opt != nil { // a nil option is ignored (V70)
			opt(&o)
		}
	}
	log := o.log.With("proto", "0.7")
	conn, err := network.Dial(address,
		network.WithLogger(log),
		network.WithReadBufferSize(o.readBufferSize),
	)
	if err != nil {
		return nil, err
	}
	s := &Session{
		conn:            conn,
		clientToken:     packet.RandomToken(),
		log:             log,
		mapCache:        o.mapCache,
		snapStorageSize: o.snapStorageSize,
		eventChanSize:   o.eventChanSize,
		readBufferSize:  o.readBufferSize,
		huff:            huffman.NewHuffman(),
		// Decompressed payload fits in one packet: DDNet CNetBase::UnpackPacket
		// decompresses into aBuffer[NET_MAX_PACKETSIZE] (1400). Pre-size so the
		// reused buffer never reallocates.
		huffBuf: make([]byte, 0, packet.MaxPacketSize),
	}
	s.localClientID.Store(-1) // unknown until Sv_ClientInfo(m_Local) (T140)
	return s, nil
}

// LocalID returns the local player's client id once learned from the 0.7
// Sv_ClientInfo message (m_Local), or -1 if not yet known (T140, V115). 0.7
// snapshots carry no local marker, so the client uses this to resolve which
// snapshot player is the local one.
func (s *Session) LocalID() int { return int(s.localClientID.Load()) }

// Capabilities returns the DDNet server capabilities announced for this session.
// DDNet sends them to sixup clients too (parsed during login, B20/V124); a
// vanilla teeworlds 0.7 server sends none, so this stays the zero value there.
func (s *Session) Capabilities() packet.ServerCapabilities {
	s.capsMu.RLock()
	defer s.capsMu.RUnlock()
	return s.caps
}

// Close sends a disconnect and closes the session.
func (s *Session) Close() error {
	s.StopReader()
	// Send CLOSE to release server slot immediately
	closePayload := []byte{MsgCtrlClose}
	pkt := BuildCtrlPacket(s.serverToken, s.ack, closePayload)
	_ = s.conn.SendRaw(pkt)
	s.log.Debug("session closed")
	return s.conn.Close()
}

// NextSeq increments and returns the next sequence number (wraps at 1024).
func (s *Session) NextSeq() int {
	s.mu.Lock()
	s.sequence = (s.sequence + 1) % packet.MaxSequence
	seq := s.sequence
	s.mu.Unlock()
	return seq
}

// Handshake performs the full 0.7 token exchange + connect handshake.
// Returns nil on success, allowing the caller to then send arbitrary packets.
func (s *Session) Handshake(ctx context.Context) error {
	// Step 1: Send token request
	tokenReq := BuildTokenRequest(s.clientToken)
	s.log.Debug("sending TOKEN request",
		"client_token", hex.EncodeToString(s.clientToken[:]),
		"size", len(tokenReq))
	sendTokenReq := func() error { return s.conn.SendRaw(tokenReq) }
	if err := sendTokenReq(); err != nil {
		return fmt.Errorf("session07: send token request: %w", err)
	}

	// Step 2: Receive token response, resending the request on loss (V68, B6);
	// SKIP any stale/unexpected control packet and keep waiting for the token
	// response, ⊥ fail on the first one (V126, B21).
	var (
		hdr  Header
		resp []byte
		err  error
	)
	for {
		resp, err = s.conn.RecvResending(ctx, packet.LoginResendInterval, sendTokenReq)
		if err != nil {
			return fmt.Errorf("session07: recv token response: %w", err)
		}
		if err := hdr.Unpack(resp); err != nil || !hdr.Flags.Control || len(resp) < 12 {
			continue
		}
		// payload starts at offset 7, ctrl msg id at 7, response token at 8-11
		if resp[7] == MsgCtrlToken {
			break
		}
		// unexpected control msg — keep waiting for the token response.
	}
	copy(s.serverToken[:], resp[8:12])
	s.log.Debug("received server token",
		"server_token", hex.EncodeToString(s.serverToken[:]))

	// Step 3: Send connect
	connectPkt := BuildConnect(s.serverToken, s.clientToken)
	s.log.Debug("sending CONNECT", "size", len(connectPkt))
	sendConnect := func() error { return s.conn.SendRaw(connectPkt) }
	if err := sendConnect(); err != nil {
		return fmt.Errorf("session07: send connect: %w", err)
	}

	// Step 4: Receive accept, resending CONNECT on loss (V68, B6). SKIP any
	// stale/duplicate control packet (e.g. a re-sent token response triggered by
	// our own token-request retransmit under loss) and keep waiting for ACCEPT,
	// rather than failing on the first non-accept packet (V126, B21).
	for {
		resp, err = s.conn.RecvResending(ctx, packet.LoginResendInterval, sendConnect)
		if err != nil {
			return fmt.Errorf("session07: recv accept: %w", err)
		}
		if err := hdr.Unpack(resp); err != nil {
			continue
		}
		if hdr.Flags.Control {
			if len(resp) >= 8 && resp[7] == MsgCtrlAccept {
				break
			}
			continue // stale token / keepalive — keep waiting
		}
		// A vital GAME chunk arrived (e.g. MAP_CHANGE): the server already
		// considers us connected, so our ACCEPT was merely lost (B21). Treat the
		// handshake as complete — recvUntilMapChange re-reads the resent vitals.
		if hdr.NumChunks > 0 {
			break
		}
	}

	s.log.Info("handshake complete",
		"server_token", hex.EncodeToString(s.serverToken[:]),
		"client_token", hex.EncodeToString(s.clientToken[:]))
	return nil
}

// Login performs the full connection sequence:
// handshake → info → (recv map_change) → ready → (recv con_ready) → startinfo + entergame
func (s *Session) Login(ctx context.Context, name, clan string, opts ...packet.LoginOption) error {
	cfg := packet.ApplyLoginOptions(opts...)
	country := cfg.Country
	if err := s.Handshake(ctx); err != nil {
		return err
	}

	// Send info
	s.log.Debug("sending INFO", "version", NetVersion)
	infoMsg := SysInfo(NetVersion, cfg.Password)
	infoChunk := WrapVitalChunk(infoMsg, s.NextSeq())
	// Resend the SAME INFO bytes (same seq) on loss — retransmission (V68, B6).
	sendInfo := func() error { return s.SendChunks(1, infoChunk) }
	if err := sendInfo(); err != nil {
		return fmt.Errorf("session07: send info: %w", err)
	}

	// Receive until MAP_CHANGE (server sends capabilities + map info after INFO); resend INFO on loss.
	if err := s.recvUntilMapChange(ctx, sendInfo); err != nil {
		return err
	}
	s.log.Debug("received MAP_CHANGE", "ack", s.ack)

	// A real teeworlds 0.7 server does NOT advance to CON_READY until the client
	// has downloaded the map (teeworlds client.cpp:1169/1199 — READY only after
	// the map is present); a bare READY is silently ignored (B11). Drain the map
	// over REQUEST_MAP_DATA/MAP_DATA here, before READY, and stash the bytes for
	// DownloadMap to parse (V110). Best-effort: on failure we still send READY so
	// the connect surfaces a clear con_ready error rather than hanging here.
	if err := s.drainMapForLogin(ctx); err != nil {
		s.log.Warn("login map download failed; sending READY anyway", "error", err)
	}

	// Send ready (now that the server has pushed the map).
	s.log.Debug("sending READY")
	readyChunk := WrapVitalChunk(SysReady(), s.NextSeq())
	sendReady := func() error { return s.SendChunks(1, readyChunk) }
	if err := sendReady(); err != nil {
		return fmt.Errorf("session07: send ready: %w", err)
	}

	// Receive until CON_READY; resend READY on loss.
	if err := s.recvUntilConReady(ctx, sendReady); err != nil {
		return err
	}
	s.log.Debug("received CON_READY", "ack", s.ack)

	// Send startinfo
	s.log.Debug("sending STARTINFO", "name", name, "clan", clan)
	startInfoMsg := GameClStartInfo(name, clan, country,
		[6]string{"standard", "", "", "standard", "standard", "standard"},
		[6]bool{true, false, false, true, true, false},
		[6]int{65408, 0, 0, 65408, 65408, 0})
	startInfoChunk := WrapVitalChunk(startInfoMsg, s.NextSeq())
	if err := s.SendChunks(1, startInfoChunk); err != nil {
		return fmt.Errorf("session07: send startinfo: %w", err)
	}

	// Send entergame
	s.log.Debug("sending ENTERGAME")
	enterChunk := WrapVitalChunk(SysEnterGame(), s.NextSeq())
	if err := s.SendChunks(1, enterChunk); err != nil {
		return fmt.Errorf("session07: send entergame: %w", err)
	}

	s.log.Info("login complete", "name", name, "ack", s.ack, "seq", s.sequence)
	return nil
}

// SendPacket sends raw bytes.
func (s *Session) SendPacket(data []byte) error {
	return s.conn.SendRaw(data)
}

// SendCtrl sends a control message.
func (s *Session) SendCtrl(payload []byte) error {
	s.mu.Lock()
	pkt := BuildCtrlPacket(s.serverToken, s.ack, payload)
	s.mu.Unlock()
	s.log.Debug("send ctrl", "ctrl_msg", payload[0], "size", len(pkt))
	return s.conn.SendRaw(pkt)
}

// SendChunks sends chunk data as a regular packet.
func (s *Session) SendChunks(numChunks int, chunkData []byte) error {
	pkt := s.BuildChunkPacket(numChunks, chunkData)
	s.log.Debug("send chunks", "num_chunks", numChunks, "size", len(pkt), "ack", s.ack)
	return s.conn.SendRaw(pkt)
}

// BuildChunkPacket builds a chunk packet using the current ack (thread-safe).
func (s *Session) BuildChunkPacket(numChunks int, chunkData []byte) []byte {
	s.mu.Lock()
	hdr := Header{
		Ack:       s.ack,
		NumChunks: numChunks,
		Token:     s.serverToken,
	}
	s.mu.Unlock()
	return append(hdr.Pack(), chunkData...)
}

// SendVitalMsg packs a message payload into a vital chunk and sends it.
func (s *Session) SendVitalMsg(payload []byte) error {
	seq := s.NextSeq()
	chunkData := WrapVitalChunk(payload, seq)
	s.log.Debug("send vital msg", "seq", seq, "payload_size", len(payload))
	return s.SendChunks(1, chunkData)
}

// SendNonVitalMsg packs a message into a non-vital chunk and sends it.
func (s *Session) SendNonVitalMsg(payload []byte) error {
	chunkData := WrapChunk(payload)
	s.log.Debug("send non-vital msg", "payload_size", len(payload))
	return s.SendChunks(1, chunkData)
}

// SendKeepAlive sends a keepalive control message.
func (s *Session) SendKeepAlive() error {
	return s.SendCtrl(CtrlKeepAlive())
}

// SendInput sends a player input message for the given tick.
// The ack game tick is clamped to the latest snapshot acked by the reader
// so the server's LastAckedSnapshot never goes backwards.
func (s *Session) SendInput(tick, predTick, inputSize int, inputData []byte) error {
	if latest := int(s.lastAckedSnap.Load()); latest > tick {
		tick = latest
	}
	inputMsg := SysInput(tick, predTick, inputSize, inputData)
	return s.SendNonVitalMsg(inputMsg)
}

// SendChat sends a chat message.
func (s *Session) SendChat(msg string) error {
	return s.SendVitalMsg(GameClSay(false, msg))
}

// SendChatTeam sends a chat message to all or team.
func (s *Session) SendChatTeam(team bool, msg string) error {
	return s.SendVitalMsg(GameClSay(team, msg))
}

// SendWhisper sends a private message. Routed via the "/whisper <id> <msg>"
// chat command for protocol-uniform behaviour.
func (s *Session) SendWhisper(toID int, msg string) error {
	return s.SendVitalMsg(GameClSay(false, fmt.Sprintf("/whisper %d %s", toID, msg)))
}

// SendKill sends the /kill command.
func (s *Session) SendKill() error {
	return s.SendVitalMsg(GameClKill())
}

// SendRconAuth sends a remote-console authentication request.
func (s *Session) SendRconAuth(password string) error {
	return s.SendVitalMsg(SysRconAuth(password))
}

// SendRconCmd sends a remote-console command (requires prior auth).
func (s *Session) SendRconCmd(cmd string) error {
	return s.SendVitalMsg(SysRconCmd(cmd))
}

// SendEmoticon shows an emoticon.
func (s *Session) SendEmoticon(e packet.Emoticon) error {
	return s.SendVitalMsg(GameClEmoticon(e))
}

// SendSetTeam requests a team change.
func (s *Session) SendSetTeam(team int) error {
	return s.SendVitalMsg(GameClSetTeam(team))
}

// SendSpectate sets the spectated client id.
func (s *Session) SendSpectate(spectatorID int) error {
	return s.SendVitalMsg(GameClSetSpectatorMode(spectatorID))
}

// SendVote casts a yes/no vote (1 = yes, -1 = no).
func (s *Session) SendVote(approve bool) error {
	v := -1
	if approve {
		v = 1
	}
	return s.SendVitalMsg(GameClVote(v))
}

// SendCallVote starts a vote.
func (s *Session) SendCallVote(voteType, value, reason string) error {
	return s.SendVitalMsg(GameClCallVote(voteType, value, reason))
}

// RecvAndAck receives one packet, tracks the ack counter, and returns the
// parsed header and the payload.
func (s *Session) RecvAndAck(ctx context.Context) (Header, []byte, error) {
	hdr, payload, err := s.recvAndParsePayload(ctx)
	if err != nil {
		return hdr, nil, err
	}
	if payload != nil && hdr.NumChunks > 0 {
		s.mu.Lock()
		oldAck := s.ack
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
		newAck := s.ack
		s.mu.Unlock()
		s.log.Debug("recv packet",
			"chunks", hdr.NumChunks,
			"size", len(payload),
			"ack", slog.GroupValue(
				slog.Int("old", oldAck),
				slog.Int("new", newAck),
			),
		)
	}
	return hdr, payload, nil
}

// recvAndAckTimeout is like RecvAndAck but uses the given read timeout.
func (s *Session) recvAndAckTimeout(ctx context.Context, timeout time.Duration) (Header, []byte, error) {
	hdr, payload, err := s.recvAndParsePayloadTimeout(ctx, timeout)
	if err != nil {
		return hdr, nil, err
	}
	if payload != nil && hdr.NumChunks > 0 {
		s.mu.Lock()
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
		s.mu.Unlock()
	}
	return hdr, payload, nil
}

// recvAndParsePayload receives a packet, parses the 0.7 header,
// and returns the header and clean payload.
func (s *Session) recvAndParsePayload(ctx context.Context) (Header, []byte, error) {
	resp, err := s.conn.RecvContext(ctx)
	if err != nil {
		return Header{}, nil, err
	}
	// Synchronous path: use the session-owned decompress buffer (V75).
	return s.parsePayload(resp, &s.huffBuf)
}

func (s *Session) recvAndParsePayloadTimeout(ctx context.Context, timeout time.Duration) (Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := s.conn.RecvContext(ctx)
	if err != nil {
		return Header{}, nil, err
	}
	// readLoop-exclusive path: use the reader-owned decompress buffer so it
	// never races the synchronous s.huffBuf (V75).
	return s.parsePayload(resp, &s.reader.huffBuf)
}

// parsePayload parses the header and returns the (decompressed) payload. buf is
// the caller's reusable decompress buffer; it is reset and grown as needed, and
// the returned payload aliases it. Each receive path passes a buffer owned by
// its own goroutine (synchronous: &s.huffBuf, reader: &s.reader.huffBuf) so the
// two never share a buffer concurrently (V75).
func (s *Session) parsePayload(resp []byte, buf *[]byte) (Header, []byte, error) {
	var hdr Header
	if err := hdr.Unpack(resp); err != nil {
		return hdr, nil, err
	}

	if HeaderSize >= len(resp) {
		return hdr, nil, nil
	}

	payload := resp[HeaderSize:]
	if hdr.Flags.Compression {
		// DecompressTo reuses *buf across calls (0 allocs steady state);
		// payload is transient and copied out by consumers (V52). s.huff is
		// only read (immutable dict), so concurrent DecompressTo calls with
		// distinct buffers are safe (V75).
		d, err := s.huff.DecompressTo((*buf)[:0], payload)
		if err == nil {
			*buf = d
			payload = d
		}
		// on error, fall back to raw payload
	}
	return hdr, payload, nil
}

// recvUntilMapChange waits for MAP_CHANGE, retransmitting the pending INFO
// vital (resend) on packet loss until the connect ctx deadline (V68, B6).
func (s *Session) recvUntilMapChange(ctx context.Context, resend func() error) error {
	for {
		resp, err := s.conn.RecvResending(ctx, packet.LoginResendInterval, resend)
		if err != nil {
			return fmt.Errorf("session07: recv waiting for map_change: %w", err)
		}
		hdr, payload, perr := s.parsePayload(resp, &s.huffBuf)
		if perr != nil || payload == nil {
			continue
		}
		if hdr.Flags.Control {
			if closed, cerr := serverClosed(payload); closed { // fail fast on rejection (V109, B10)
				return cerr
			}
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
		// DDNet sends the capabilities EX before/with MAP_CHANGE, during this
		// synchronous handshake (the reader isn't up yet) — scan for it (B20/V124).
		for _, ex := range packet.ExtractAllSysMsgPayloads(payload, MsgSysEx, Split) {
			s.maybeParseCapabilities(ex)
		}
		if data := packet.ExtractSysMsgPayload(payload, MsgSysMapChange, Split); data != nil {
			if info, err := packet.ParseMapChangePayload(data); err == nil {
				s.mapMu.Lock()
				s.mapInfo = info
				s.parsed = nil
				s.mapMu.Unlock()
				s.log.Debug("parsed MAP_CHANGE", "map", info.Name, "crc", info.CRC, "size", info.Size, "sha256", hex.EncodeToString(info.Sha256[:]))
			}
			return nil
		}
	}
}

// serverClosed reports whether a control payload is a CTRL_CLOSE and, if so, the
// classified error carrying the server's reason text (V109, B10).
func serverClosed(payload []byte) (bool, error) {
	if len(payload) > 0 && payload[0] == MsgCtrlClose {
		reason := ""
		if len(payload) > 1 {
			reason = string(payload[1:])
		}
		return true, &packet.ServerClosedError{Reason: reason}
	}
	return false, nil
}

// recvUntilConReady waits for CON_READY, retransmitting the pending READY vital
// (resend) on packet loss until the connect ctx deadline (V68, B6).
func (s *Session) recvUntilConReady(ctx context.Context, resend func() error) error {
	for {
		resp, err := s.conn.RecvResending(ctx, packet.LoginResendInterval, resend)
		if err != nil {
			return fmt.Errorf("session07: recv waiting for con_ready: %w", err)
		}
		hdr, payload, perr := s.parsePayload(resp, &s.huffBuf)
		if perr != nil || payload == nil {
			continue
		}
		if hdr.Flags.Control {
			if closed, cerr := serverClosed(payload); closed { // fail fast on rejection (V109, B10)
				return cerr
			}
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
		// Parse MAP_CHANGE if present (server may send it here on map reload)
		if data := packet.ExtractSysMsgPayload(payload, MsgSysMapChange, Split); data != nil {
			if info, err := packet.ParseMapChangePayload(data); err == nil {
				s.mapMu.Lock()
				s.mapInfo = info
				s.parsed = nil
				s.mapMu.Unlock()
				s.log.Debug("parsed MAP_CHANGE", "map", info.Name, "crc", info.CRC, "size", info.Size, "sha256", hex.EncodeToString(info.Sha256[:]))
			}
		}
		if packet.ContainsSysMsg(payload, MsgSysConReady, Split) {
			return nil
		}
	}
}

// drainMapForLogin downloads the map over REQUEST_MAP_DATA/MAP_DATA during the
// login handshake (before READY) so a real 0.7 server advances to CON_READY
// (B11, V110). It only RECEIVES and stashes the raw bytes (loginMapData) for
// DownloadMap to parse later — it deliberately does NOT parse here, so a
// malformed/large map can never fail or stall the connect at parse time.
func (s *Session) drainMapForLogin(ctx context.Context) error {
	info := s.MapInfo()
	if info.Name == "" || info.Size <= 0 {
		return nil // no map advertised → nothing to download, send READY as-is
	}
	// The server sends NumChunksPerRequest MAP_DATA chunks per REQUEST_MAP_DATA
	// (sv_map_window). We must request ONE window, receive exactly that many
	// chunks, then request the next (teeworlds client.cpp:1247). Flooding a
	// request per packet desyncs + crashes a vanilla 0.7 server (B12).
	perReq := info.NumChunksPerRequest
	if perReq <= 0 {
		perReq = 1
	}
	sendReq := func() error {
		return s.SendChunks(1, WrapVitalChunk(SysRequestMapData(), s.NextSeq()))
	}
	if err := sendReq(); err != nil {
		return err
	}

	var data []byte
	chunkCount := 0
	safety := info.Size*2 + 4096 // backstop vs an infinite loop; ctx is the real bound
	for len(data) < info.Size && safety > 0 {
		safety--
		// Time-boxed recv: on timeout (a lost REQUEST_MAP_DATA or a lost chunk)
		// resend the current request. The request carries our ack, so the server
		// resends the unacked MAP_DATA vitals from there (T162/V126/B21).
		hdr, payload, err := s.recvAndParsePayloadTimeout(ctx, packet.LoginResendInterval)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("session07: map download: %w", ctx.Err())
			}
			if rerr := sendReq(); rerr != nil {
				return rerr
			}
			continue
		}
		if payload == nil {
			continue
		}
		if hdr.Flags.Control {
			if closed, cerr := serverClosed(payload); closed {
				return cerr
			}
			continue
		}
		// In-order vital reassembly: accept each vital chunk exactly once, in
		// sequence (advancing s.ack on the next-expected seq, mirroring
		// CountVitalChunks), and append the MAP_DATA bytes. A duplicate/retransmit
		// (seq ≤ ack) or out-of-order future chunk is skipped — the server resends
		// it until our ack catches up — so the map is never double-appended under
		// loss (T162).
		for _, ch := range packet.UnpackChunks(payload, Split) {
			if !ch.Header.Flags.Vital || ch.Header.Seq != (s.ack+1)%packet.MaxSequence {
				continue
			}
			s.ack = ch.Header.Seq
			if len(ch.Data) < 1 {
				continue
			}
			msgRaw := int(ch.Data[0] & 0x3F)
			if ch.Data[0]&0x40 != 0 {
				msgRaw = ^msgRaw
			}
			if msgRaw&1 == 0 || msgRaw>>1 != MsgSysMapData { // sys + MAP_DATA only
				continue
			}
			data = append(data, ch.Data[1:]...)
			chunkCount++
			if chunkCount%perReq == 0 && len(data) < info.Size { // window done → next
				if err := sendReq(); err != nil {
					return err
				}
			}
		}
	}
	s.loginMapData = data
	s.log.Debug("login map drained", "bytes", len(data), "want", info.Size, "chunks", chunkCount)
	return nil
}

// DownloadMap requests the map from the server, reassembles the data,
// and parses it with twmap.Parse. The result is stored in the session
// and can be retrieved with Map().
//
// 0.7 map download is server-push: the client sends REQUEST_MAP_DATA
// and the server pushes sv_map_window chunks at a time. MAP_DATA messages
// contain only raw bytes (no header fields like 0.6).
func (s *Session) DownloadMap(ctx context.Context) (*twmap.Map, error) {
	info := s.MapInfo()
	if info.Name == "" {
		return nil, fmt.Errorf("session07: no map info (call Login first)")
	}

	// Try the shared cache first
	if s.mapCache != nil {
		m, ok := s.mapCache.GetOrWait(info.Name, info.Sha256)
		if ok && m != nil {
			s.log.Info("map cache hit", "map", info.Name)
			s.mapMu.Lock()
			s.parsed = m
			s.mapMu.Unlock()
			return m, nil
		}
		// ok==false means we are the designated downloader
	}

	// Login already drained the map bytes before READY (B11/V110); the server
	// pushes the map only once, during CONNECTING, so parse those instead of
	// re-requesting (which an in-game server would ignore).
	if len(s.loginMapData) > 0 {
		data := s.loginMapData
		s.loginMapData = nil
		var m *twmap.Map
		var perr error
		if s.mapCache != nil {
			m, perr = s.mapCache.ParseAndPut(info.Name, info.Sha256, data)
		} else {
			m, perr = twmap.Parse(bytes.NewReader(data))
		}
		if perr != nil {
			return nil, fmt.Errorf("session07: parse map %q: %w", info.Name, perr)
		}
		s.mapMu.Lock()
		s.parsed = m
		s.mapMu.Unlock()
		return m, nil
	}

	var mapData []byte
	requestsLeft := (info.Size / 768) + 2 // safety limit

	for len(mapData) < info.Size && requestsLeft > 0 {
		requestsLeft--
		// Send REQUEST_MAP_DATA to trigger the server to push the next window
		reqMsg := SysRequestMapData() // chunk index ignored by Sixup server
		reqChunk := WrapVitalChunk(reqMsg, s.NextSeq())
		if err := s.SendChunks(1, reqChunk); err != nil {
			if s.mapCache != nil {
				s.mapCache.PutFailed(info.Name, info.Sha256)
			}
			return nil, fmt.Errorf("session07: request map data: %w", err)
		}

		// Receive chunks pushed by the server for this window
		chunkData, err := s.recvMapDataChunks(ctx)
		if err != nil {
			if s.mapCache != nil {
				s.mapCache.PutFailed(info.Name, info.Sha256)
			}
			return nil, fmt.Errorf("session07: recv map data: %w", err)
		}
		mapData = append(mapData, chunkData...)
	}

	s.log.Info("map download complete", "map", info.Name, "bytes", len(mapData))

	// Parse and optionally store in cache
	var m *twmap.Map
	var parseErr error
	if s.mapCache != nil {
		m, parseErr = s.mapCache.ParseAndPut(info.Name, info.Sha256, mapData)
	} else {
		m, parseErr = twmap.Parse(bytes.NewReader(mapData))
	}
	if parseErr != nil {
		return nil, fmt.Errorf("session07: parse map %q: %w", info.Name, parseErr)
	}

	s.mapMu.Lock()
	s.parsed = m
	s.mapMu.Unlock()

	return m, nil
}

// recvMapDataChunks receives packets until at least one MAP_DATA is found
// or a timeout occurs. Returns the concatenated raw map bytes.
// In 0.7, MAP_DATA contains only raw bytes (no header fields).
func (s *Session) recvMapDataChunks(ctx context.Context) ([]byte, error) {
	var mapData []byte
	for range 30 {
		hdr, payload, err := s.recvAndParsePayload(ctx)
		if err != nil {
			if mapData != nil {
				return mapData, nil // timeout after receiving some data is OK
			}
			return nil, err
		}
		if payload == nil {
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)

		// Extract all MAP_DATA chunks from this packet
		chunks := packet.UnpackChunks(payload, Split)
		gotData := false
		for _, ch := range chunks {
			if len(ch.Data) < 1 {
				continue
			}
			msgRaw := int(ch.Data[0] & 0x3F)
			if ch.Data[0]&0x40 != 0 {
				msgRaw = ^msgRaw
			}
			sys := msgRaw&1 != 0
			msgID := msgRaw >> 1
			if sys && msgID == MsgSysMapData {
				mapData = append(mapData, ch.Data[1:]...)
				gotData = true
			}
		}
		if gotData {
			return mapData, nil
		}
	}
	if mapData != nil {
		return mapData, nil
	}
	return nil, fmt.Errorf("session07: did not receive MAP_DATA")
}

// Map returns the parsed map, or nil if no map has been set.
func (s *Session) Map() *twmap.Map {
	s.mapMu.RLock()
	defer s.mapMu.RUnlock()
	return s.parsed
}

// MapName returns the current map name.
func (s *Session) MapName() string {
	s.mapMu.RLock()
	defer s.mapMu.RUnlock()
	return s.mapInfo.Name
}

// MapInfo returns the current map metadata.
func (s *Session) MapInfo() packet.MapInfo {
	s.mapMu.RLock()
	defer s.mapMu.RUnlock()
	return s.mapInfo
}

// SetMap replaces the parsed map (thread-safe). Also stores the result
// in the shared MapCache so other sessions can reuse it.
func (s *Session) SetMap(m *twmap.Map, info packet.MapInfo) {
	s.mapMu.Lock()
	s.mapInfo = info
	s.parsed = m
	s.mapMu.Unlock()
	if m != nil && info.Name != "" && s.mapCache != nil {
		s.mapCache.Put(info.Name, info.Sha256, m)
	}
}
