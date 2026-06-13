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
type Option func(*Session)

// WithLogger sets a custom logger for the session.
// Without this option, logging is silently discarded.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Session) {
		if logger != nil {
			s.log = logger.With("proto", "0.7")
		}
	}
}

// WithMapCache sets a shared map cache. Multiple sessions using the same
// cache will deduplicate downloads: only the first session to request a
// map actually downloads it; the rest wait and reuse the cached result.
func WithMapCache(cache *packet.MapCache) Option {
	return func(s *Session) {
		if cache != nil {
			s.mapCache = cache
		}
	}
}

// WithSnapStorageSize sets the retained-snapshot window (packet.SnapStorage
// MaxSnaps) used by the reader for delta decompression (V53). Zero or unset
// keeps the default; the value is validated by packet.WithMaxSnaps.
func WithSnapStorageSize(n int) Option {
	return func(s *Session) { s.snapStorageSize = n }
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
	mapMu         sync.RWMutex
	mapInfo       packet.MapInfo
	parsed        *twmap.Map
	mapCache      *packet.MapCache // always set: shared or per-session
	reader        reader           // background reader state (activated by StartReader)

	snapStorageSize int // configured packet.SnapStorage window; 0 = default (V53)
}

// NewSession creates a new 0.7 session against the given address.
// By default, logging is discarded. Use WithLogger to customize.
func NewSession(address string, opts ...Option) (*Session, error) {
	tmp := &Session{log: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(tmp)
	}
	conn, err := network.Dial(address, network.WithLogger(tmp.log))
	if err != nil {
		return nil, err
	}
	s := &Session{
		conn:        conn,
		clientToken: packet.RandomToken(),
		log:         tmp.log.With("proto", "0.7"),
		mapCache:    packet.NewMapCache(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Capabilities returns the DDNet server capabilities. 0.7 (sixup) servers do
// not send the DDNet capabilities message, so this is always the zero value.
func (s *Session) Capabilities() packet.ServerCapabilities {
	return packet.ServerCapabilities{}
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
	if err := s.conn.SendRaw(tokenReq); err != nil {
		return fmt.Errorf("session07: send token request: %w", err)
	}

	// Step 2: Receive token response
	resp, err := s.conn.RecvContext(ctx)
	if err != nil {
		return fmt.Errorf("session07: recv token response: %w", err)
	}

	var hdr Header
	if err := hdr.Unpack(resp); err != nil {
		return fmt.Errorf("session07: unpack token response header: %w", err)
	}
	if !hdr.Flags.Control || len(resp) < 12 {
		return fmt.Errorf("session07: unexpected token response (flags=%+v len=%d)", hdr.Flags, len(resp))
	}
	// payload starts at offset 7, ctrl msg id at 7, response token at 8-11
	if resp[7] != MsgCtrlToken {
		return fmt.Errorf("session07: expected ctrl token msg, got 0x%02x", resp[7])
	}
	copy(s.serverToken[:], resp[8:12])
	s.log.Debug("received server token",
		"server_token", hex.EncodeToString(s.serverToken[:]))

	// Step 3: Send connect
	connectPkt := BuildConnect(s.serverToken, s.clientToken)
	s.log.Debug("sending CONNECT", "size", len(connectPkt))
	if err := s.conn.SendRaw(connectPkt); err != nil {
		return fmt.Errorf("session07: send connect: %w", err)
	}

	// Step 4: Receive accept
	resp, err = s.conn.RecvContext(ctx)
	if err != nil {
		return fmt.Errorf("session07: recv accept: %w", err)
	}
	if err := hdr.Unpack(resp); err != nil {
		return fmt.Errorf("session07: unpack accept header: %w", err)
	}
	if !hdr.Flags.Control || len(resp) < 8 {
		return fmt.Errorf("session07: unexpected accept response")
	}
	if resp[7] != MsgCtrlAccept {
		return fmt.Errorf("session07: expected accept msg, got 0x%02x", resp[7])
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
	if err := s.SendChunks(1, infoChunk); err != nil {
		return fmt.Errorf("session07: send info: %w", err)
	}

	// Receive until MAP_CHANGE (server sends capabilities + map info after INFO)
	if err := s.recvUntilMapChange(ctx); err != nil {
		return err
	}
	s.log.Debug("received MAP_CHANGE", "ack", s.ack)

	// Send ready (signals we have the map / don't need download)
	s.log.Debug("sending READY")
	readyChunk := WrapVitalChunk(SysReady(), s.NextSeq())
	if err := s.SendChunks(1, readyChunk); err != nil {
		return fmt.Errorf("session07: send ready: %w", err)
	}

	// Receive until CON_READY
	if err := s.recvUntilConReady(ctx); err != nil {
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

// DrainResponses receives up to n packets, updating ack as it goes,
// and returns all payloads collected.
func (s *Session) DrainResponses(n int) [][]byte {
	drainTimeout := max(s.conn.ReadTimeout()/10, 5*time.Millisecond)
	ctx := context.Background()

	var payloads [][]byte
	for range n {
		_, payload, err := s.recvAndAckTimeout(ctx, drainTimeout)
		if err != nil {
			break
		}
		if payload != nil {
			payloads = append(payloads, payload)
		}
	}
	return payloads
}

// recvAndParsePayload receives a packet, parses the 0.7 header,
// and returns the header and clean payload.
func (s *Session) recvAndParsePayload(ctx context.Context) (Header, []byte, error) {
	resp, err := s.conn.RecvContext(ctx)
	if err != nil {
		return Header{}, nil, err
	}
	return s.parsePayload(resp)
}

func (s *Session) recvAndParsePayloadTimeout(ctx context.Context, timeout time.Duration) (Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := s.conn.RecvContext(ctx)
	if err != nil {
		return Header{}, nil, err
	}
	return s.parsePayload(resp)
}

func (s *Session) parsePayload(resp []byte) (Header, []byte, error) {
	var hdr Header
	if err := hdr.Unpack(resp); err != nil {
		return hdr, nil, err
	}

	if HeaderSize >= len(resp) {
		return hdr, nil, nil
	}

	payload := resp[HeaderSize:]
	if hdr.Flags.Compression {
		d, err := huffman.Decompress(payload)
		if err == nil {
			payload = d
		}
		// on error, fall back to raw payload
	}
	return hdr, payload, nil
}

func (s *Session) recvUntilMapChange(ctx context.Context) error {
	for range 20 {
		hdr, payload, err := s.recvAndParsePayload(ctx)
		if err != nil {
			return fmt.Errorf("session07: recv waiting for map_change: %w", err)
		}
		if payload == nil {
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
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
	return fmt.Errorf("session07: did not receive MAP_CHANGE")
}

func (s *Session) recvUntilConReady(ctx context.Context) error {
	for range 20 {
		hdr, payload, err := s.recvAndParsePayload(ctx)
		if err != nil {
			return fmt.Errorf("session07: recv waiting for con_ready: %w", err)
		}
		if payload == nil {
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
		if hdr.Flags.Control {
			continue
		}
		// Parse MAP_CHANGE if present (server may sends it here on map reload)
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
	return fmt.Errorf("session07: did not receive CON_READY")
}

func (s *Session) recvUntilReadyToEnter(ctx context.Context) error {
	for range 30 {
		hdr, payload, err := s.recvAndParsePayload(ctx)
		if err != nil {
			return fmt.Errorf("session07: recv waiting for ready_to_enter: %w", err)
		}
		if payload == nil {
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
		if packet.ContainsGameMsg(payload, MsgGameSvReadyToEnter, Split) {
			return nil
		}
	}
	return fmt.Errorf("session07: did not receive SV_READYTOENTER")
}

// DownloadMap requests the map from the server, reassembles the data,
// and parses it with twmap.Parse. The result is stored in the session
// and can be retrieved with Map().
//
// 0.7 map download is server-push: the client sends REQUEST_MAP_DATA
// and the server pushes sv_map_window chunks at a time. MAP_DATA messages
// contain only raw bytes (no header fields like 0.6).
func (s *Session) DownloadMap(ctx context.Context) (*twmap.Map, error) {
	info := s.GetMapInfo()
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

	var mapData []byte
	requestsLeft := (info.Size / 768) + 2 // safety limit

	for len(mapData) < info.Size && requestsLeft > 0 {
		requestsLeft--
		// Send REQUEST_MAP_DATA to trigger the server to push the next window
		reqMsg := SysRequestMapData(0) // chunk index ignored by Sixup server
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

// GetMapInfo returns the current map metadata.
func (s *Session) GetMapInfo() packet.MapInfo {
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
