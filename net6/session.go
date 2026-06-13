package net6

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
	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
	"github.com/teeworlds-go/huffman/v2"
)

// securityTokenMagic is the DDNet "TKEN" magic identifying token-capable peers.
var securityTokenMagic = [4]byte{'T', 'K', 'E', 'N'}

// SecurityTokenUnknown signals that no security token was received.
var SecurityTokenUnknown = [4]byte{0xFF, 0xFF, 0xFF, 0xFF}

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

// Session tracks the connection state for a 0.6 / DDNet client session.
type Session struct {
	conn          *network.Conn
	clientToken   packet.Token
	serverToken   packet.Token // 0.6.5 header token
	securityToken [4]byte      // DDNet security token (appended to packet data)
	ack           int
	sequence      int
	log           *slog.Logger

	mu            sync.Mutex // protects ack and sequence for concurrent read/write
	lastAckedSnap atomic.Int64
	mapMu         sync.RWMutex
	mapInfo       packet.MapInfo
	parsed        *twmap.Map
	mapCache      *packet.MapCache // always set: shared or per-session
	reader        reader           // background reader state (activated by StartReader)

	snapStorageSize int // configured packet.SnapStorage window; 0 = default (V53)
	eventChanSize   int // configured reader event-channel buffer; 0 = default (V54)
	readBufferSize  int // configured UDP receive-buffer size; 0 = default (V54)

	capsMu sync.RWMutex
	caps   packet.ServerCapabilities // DDNet server capabilities (T33, V47)

	huff    *huffman.Huffman // shared decompressor (precomputed dict, read-only)
	huffBuf []byte           // synchronous-path Decompress buffer; reader uses its own (V75)
}

// Capabilities returns the DDNet server capabilities announced for this
// session, or the zero value if the server has not sent them.
func (s *Session) Capabilities() packet.ServerCapabilities {
	s.capsMu.RLock()
	defer s.capsMu.RUnlock()
	return s.caps
}

// NewSession creates a new 0.6 session against the given address.
// By default, logging is discarded and each session has its own map cache.
// Use WithLogger and WithMapCache to customize.
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
	log := o.log.With("proto", "0.6")
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
		securityToken:   SecurityTokenUnknown,
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
	return s, nil
}

// Close sends a disconnect and closes the session.
func (s *Session) Close() error {
	s.StopReader()
	// Send CLOSE to release server slot immediately
	closePayload := []byte{MsgCtrlClose}
	pkt := BuildCtrlPacketNoToken(s.ack, closePayload)
	_ = s.conn.SendRaw(s.appendSecurityToken(pkt))
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

// HasSecurityToken returns true if a DDNet security token was negotiated.
func (s *Session) HasSecurityToken() bool {
	return s.securityToken != SecurityTokenUnknown
}

// appendSecurityToken appends the DDNet security token to a packet if one was negotiated.
func (s *Session) appendSecurityToken(pkt []byte) []byte {
	if s.HasSecurityToken() {
		return append(pkt, s.securityToken[:]...)
	}
	return pkt
}

// sendPacket sends a packet, appending the DDNet security token if negotiated.
func (s *Session) sendPacket(pkt []byte) error {
	return s.conn.SendRaw(s.appendSecurityToken(pkt))
}

// Handshake performs the 0.6 / DDNet connect handshake.
//
// DDNet flow:
//
//	client → server: NET_CTRLMSG_CONNECT (with "TKEN" magic + client token in extra data)
//	server → client: NET_CTRLMSG_CONNECTACCEPT (with "TKEN" magic + security token)
//	client → server: NET_CTRLMSG_ACCEPT (with security token echo)
func (s *Session) Handshake(ctx context.Context) error {
	// Build CONNECT with TKEN magic at extra data offset 0 and client token at offset 4
	connectPayload := make([]byte, 1+512)
	connectPayload[0] = MsgCtrlConnect
	// Write "TKEN" magic at extra data offset 0 (= payload offset 1)
	copy(connectPayload[1:5], securityTokenMagic[:])
	// Write client token at extra data offset 4 (= payload offset 5)
	copy(connectPayload[5:9], s.clientToken[:])

	connectPkt := BuildCtrlPacketNoToken(0, connectPayload)
	s.log.Debug("sending CONNECT",
		"client_token", hex.EncodeToString(s.clientToken[:]),
		"size", len(connectPkt))
	sendConnect := func() error { return s.conn.SendRaw(connectPkt) }
	if err := sendConnect(); err != nil {
		return fmt.Errorf("session06: send connect: %w", err)
	}

	// Receive connect accept, resending CONNECT on loss (V68, B6).
	resp, err := s.conn.RecvResending(ctx, packet.LoginResendInterval, sendConnect)
	if err != nil {
		return fmt.Errorf("session06: recv connect accept: %w", err)
	}

	var hdr Header
	if err := hdr.Unpack(resp); err != nil {
		return fmt.Errorf("session06: unpack connect accept header: %w", err)
	}

	payloadStart := hdr.Size()
	if len(resp) < payloadStart+1 {
		return fmt.Errorf("session06: connect accept too short (%d bytes)", len(resp))
	}

	if resp[payloadStart] != MsgCtrlConnectAccept {
		return fmt.Errorf("session06: expected CONNECTACCEPT (0x02), got 0x%02x", resp[payloadStart])
	}

	payload := resp[payloadStart:]

	// Check for DDNet "TKEN" magic in the CONNECTACCEPT
	if len(payload) >= 1+4+4 &&
		bytes.Equal(payload[1:5], securityTokenMagic[:]) {
		// DDNet security token follows the TKEN magic
		copy(s.securityToken[:], payload[5:9])
		// DDNet uses the security token as the header token too
		s.serverToken = packet.Token(s.securityToken)
		s.log.Info("handshake complete (DDNet TKEN)",
			"security_token", hex.EncodeToString(s.securityToken[:]),
			"server_token", hex.EncodeToString(s.serverToken[:]))
	} else if len(payload) >= 5 {
		// Vanilla 0.6.5: server token directly after ctrl msg byte
		copy(s.serverToken[:], payload[1:5])
		s.log.Info("handshake complete (vanilla 0.6.5)",
			"server_token", hex.EncodeToString(s.serverToken[:]))
	}

	// Step 3 (DDNet): Send ACCEPT with the token to finalize the connection
	if s.HasSecurityToken() {
		acceptPayload := make([]byte, 1+4)
		acceptPayload[0] = MsgCtrlAccept
		copy(acceptPayload[1:5], s.securityToken[:])
		acceptPkt := BuildCtrlPacketNoToken(0, acceptPayload)
		s.log.Debug("sending ACCEPT", "size", len(acceptPkt))
		if err := s.conn.SendRaw(acceptPkt); err != nil {
			return fmt.Errorf("session06: send accept: %w", err)
		}
	}

	return nil
}

// Login performs the full connection sequence:
// handshake → info → (recv map_change) → ready → (recv con_ready) → startinfo + entergame
func (s *Session) Login(ctx context.Context, name, clan string, opts ...packet.LoginOption) error {
	cfg := packet.ApplyLoginOptions(opts...)
	skin, country := cfg.Skin, cfg.Country
	if err := s.Handshake(ctx); err != nil {
		return err
	}

	s.log.Debug("sending INFO", "version", NetVersion)
	// Send CLIENTVER (DDNet extension) + INFO together
	clientVerMsg := SysClientVer()
	clientVerChunk := WrapVitalChunk(clientVerMsg, s.NextSeq())
	infoMsg := SysInfo(NetVersion, cfg.Password)
	infoChunk := WrapVitalChunk(infoMsg, s.NextSeq())
	combined := append(clientVerChunk, infoChunk...)
	// Resend the SAME INFO bytes (same seq) on loss — retransmission, ⊥ a new vital.
	sendInfo := func() error { return s.SendChunks(2, combined) }
	if err := sendInfo(); err != nil {
		return fmt.Errorf("session06: send clientver+info: %w", err)
	}

	// Receive until MAP_CHANGE (server sends map info after INFO); resend INFO on loss.
	if err := s.recvUntilMapChange(ctx, sendInfo); err != nil {
		return err
	}
	s.log.Debug("received MAP_CHANGE", "ack", s.ack)

	// Send ready (signals we have the map / don't need download)
	s.log.Debug("sending READY", "seq", s.sequence+1)
	readyChunk := WrapVitalChunk(SysReady(), s.NextSeq())
	sendReady := func() error { return s.SendChunks(1, readyChunk) }
	if err := sendReady(); err != nil {
		return fmt.Errorf("session06: send ready: %w", err)
	}

	// Receive until CON_READY; resend READY on loss.
	if err := s.recvUntilConReady(ctx, sendReady); err != nil {
		return err
	}
	s.log.Debug("received CON_READY", "ack", s.ack)

	// Send startinfo
	s.log.Debug("sending STARTINFO", "name", name, "clan", clan, "skin", skin)
	startInfoMsg := GameClStartInfo(name, clan, country, skin, true, 65408, 65408)
	startInfoChunk := WrapVitalChunk(startInfoMsg, s.NextSeq())
	if err := s.SendChunks(1, startInfoChunk); err != nil {
		return fmt.Errorf("session06: send startinfo: %w", err)
	}

	// Send entergame
	s.log.Debug("sending ENTERGAME")
	enterChunk := WrapVitalChunk(SysEnterGame(), s.NextSeq())
	if err := s.SendChunks(1, enterChunk); err != nil {
		return fmt.Errorf("session06: send entergame: %w", err)
	}

	s.log.Info("login complete", "name", name, "ack", s.ack, "seq", s.sequence)
	return nil
}

// SendPacket sends raw bytes.
func (s *Session) SendPacket(data []byte) error {
	return s.conn.SendRaw(data)
}

// SendCtrl sends a control message with the security token appended.
func (s *Session) SendCtrl(payload []byte) error {
	s.mu.Lock()
	pkt := BuildCtrlPacketNoToken(s.ack, payload)
	s.mu.Unlock()
	s.log.Debug("send ctrl", "ctrl_msg", payload[0], "size", len(pkt))
	return s.sendPacket(pkt)
}

// SendChunks sends chunk data as a regular packet with the security token appended.
func (s *Session) SendChunks(numChunks int, chunkData []byte) error {
	pkt := s.BuildChunkPacket(numChunks, chunkData)
	s.log.Debug("send chunks", "num_chunks", numChunks, "size", len(pkt), "ack", s.ack)
	return s.conn.SendRaw(pkt)
}

// BuildChunkPacket builds a chunk packet with the security token appended.
func (s *Session) BuildChunkPacket(numChunks int, chunkData []byte) []byte {
	s.mu.Lock()
	hdr := Header{
		Ack:       s.ack,
		NumChunks: numChunks,
	}
	s.mu.Unlock()
	pkt := append(hdr.Pack(), chunkData...)
	return s.appendSecurityToken(pkt)
}

// SendVitalMsg packs a message into a vital chunk and sends it.
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

// RecvAndAck receives one packet, tracks the ack counter, and returns the
// parsed header and the payload with security token stripped.
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
			"compressed", hdr.Flags.Compression,
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

// stripSecurityToken removes the appended 4-byte security token from incoming payload
// (DDNet appends security tokens to all packets).
func (s *Session) stripSecurityToken(payload []byte) []byte {
	if s.HasSecurityToken() && len(payload) >= 4 {
		return payload[:len(payload)-4]
	}
	return payload
}

// recvAndParsePayload receives a packet using the default timeout,
// parses the header, strips the security token, decompresses if needed,
// and returns the header and the clean payload.
func (s *Session) recvAndParsePayload(ctx context.Context) (Header, []byte, error) {
	resp, err := s.conn.RecvContext(ctx)
	if err != nil {
		return Header{}, nil, err
	}
	// Synchronous path: use the session-owned decompress buffer (V75).
	return s.parsePayload(resp, &s.huffBuf)
}

// recvAndParsePayloadTimeout is like recvAndParsePayload but uses the given timeout.
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

// parsePayload parses a received packet: header, decompress, strip token. buf is
// the caller's reusable decompress buffer (reset and grown as needed); the
// returned payload aliases it. Each receive path passes a buffer owned by its
// own goroutine (synchronous: &s.huffBuf, reader: &s.reader.huffBuf) so the two
// never share a buffer concurrently (V75).
func (s *Session) parsePayload(resp []byte, buf *[]byte) (Header, []byte, error) {
	var hdr Header
	if err := hdr.Unpack(resp); err != nil {
		return hdr, nil, err
	}

	payloadStart := hdr.Size()
	if payloadStart >= len(resp) {
		return hdr, nil, nil
	}

	payload := resp[payloadStart:]

	// DDNet: for non-control packets, the server may set the compression flag.
	// The security token is inside the compressed data, so we need to
	// decompress first, then strip. If decompression fails (e.g. snap data
	// with edge-case huffman patterns), fall back to raw payload.
	if hdr.Flags.Compression && !hdr.Flags.Control {
		// DecompressTo reuses *buf across calls (0 allocs steady state);
		// payload is transient and copied out by consumers (V52). s.huff is
		// only read (immutable dict), so concurrent DecompressTo calls with
		// distinct buffers are safe (V75).
		// TODO: migrate to DecompressTo((*buf)[:0], payload)
		d, err := s.huff.Decompress(payload)
		if err == nil {
			*buf = d
			payload = d
		} else {
			s.log.Debug("decompress failed, using raw payload",
				"compressed_size", len(payload), "error", err)
		}
	}

	payload = s.stripSecurityToken(payload)
	return hdr, payload, nil
}

// RecvUntilMapChange waits for a MAP_CHANGE system message, extracts
// the map metadata, and stores it in the session.
func (s *Session) RecvUntilMapChange(ctx context.Context) error {
	return s.recvUntilMapChange(ctx, func() error { return nil })
}

// recvUntilMapChange waits for MAP_CHANGE, retransmitting the pending vital
// (resend, the INFO chunk) on packet loss until the connect ctx deadline (V68).
func (s *Session) recvUntilMapChange(ctx context.Context, resend func() error) error {
	for {
		resp, err := s.conn.RecvResending(ctx, packet.LoginResendInterval, resend)
		if err != nil {
			return fmt.Errorf("session06: recv waiting for map_change: %w", err)
		}
		hdr, payload, perr := s.parsePayload(resp, &s.huffBuf)
		if perr != nil || payload == nil {
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
		// DDNet sends its capabilities (NETMSG_EX) before MAP_CHANGE; capture it
		// here since the background reader is not running yet (V47).
		for _, ex := range packet.ExtractAllSysMsgPayloads(payload, MsgSysEx, Split) {
			s.maybeParseCapabilities(ex)
		}
		if data := packet.ExtractSysMsgPayload(payload, MsgSysMapChange, Split); data != nil {
			if info, err := packet.ParseMapChangePayload(data); err == nil {
				s.mapMu.Lock()
				s.mapInfo = info
				s.parsed = nil // invalidate until downloaded
				s.mapMu.Unlock()
				s.log.Debug("parsed MAP_CHANGE", "map", info.Name, "crc", info.CRC, "size", info.Size, "sha256", hex.EncodeToString(info.Sha256[:]))
			}
			return nil
		}
	}
}

// recvUntilConReady waits for CON_READY, retransmitting the pending vital
// (resend, the READY chunk) on packet loss until the connect ctx deadline (V68).
func (s *Session) recvUntilConReady(ctx context.Context, resend func() error) error {
	for {
		resp, err := s.conn.RecvResending(ctx, packet.LoginResendInterval, resend)
		if err != nil {
			return fmt.Errorf("session06: recv waiting for con_ready: %w", err)
		}
		hdr, payload, perr := s.parsePayload(resp, &s.huffBuf)
		if perr != nil || payload == nil {
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)
		if packet.ContainsSysMsg(payload, MsgSysConReady, Split) {
			return nil
		}
	}
}

// MapName returns the current map name (from the last MAP_CHANGE).
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

// Map returns the parsed map, or nil if no map has been downloaded yet.
func (s *Session) Map() *twmap.Map {
	s.mapMu.RLock()
	defer s.mapMu.RUnlock()
	return s.parsed
}

// DownloadMap requests the map from the server chunk by chunk, reassembles
// the data, and parses it with twmap.Parse. The result is stored in the
// session and can be retrieved with Map().
//
// If a MapCache is set and already contains the map, the download is
// skipped entirely. If another goroutine is downloading the same map,
// this call blocks until the download completes.
//
// MAP_DATA format: Int(last) + Int(crc) + Int(chunk) + Int(chunkSize) + Raw(data)
func (s *Session) DownloadMap(ctx context.Context) (*twmap.Map, error) {
	info := s.MapInfo()
	if info.Name == "" {
		return nil, fmt.Errorf("session06: no map info (call Login or recvUntilMapChange first)")
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
	chunkIdx := 0
	maxChunks := (info.Size / 896) + 2 // safety limit

	for chunkIdx < maxChunks {
		// Request next chunk
		reqMsg := SysRequestMapData(chunkIdx)
		reqChunk := WrapVitalChunk(reqMsg, s.NextSeq())
		if err := s.SendChunks(1, reqChunk); err != nil {
			if s.mapCache != nil {
				s.mapCache.PutFailed(info.Name, info.Sha256)
			}
			return nil, fmt.Errorf("session06: request map data chunk %d: %w", chunkIdx, err)
		}

		// Receive until we get a MAP_DATA response
		last, chunkData, err := s.recvMapDataChunk(ctx)
		if err != nil {
			if s.mapCache != nil {
				s.mapCache.PutFailed(info.Name, info.Sha256)
			}
			return nil, fmt.Errorf("session06: recv map data chunk %d: %w", chunkIdx, err)
		}
		mapData = append(mapData, chunkData...)
		chunkIdx++

		if last != 0 {
			break
		}
	}

	s.log.Info("map download complete", "map", info.Name, "chunks", chunkIdx, "bytes", len(mapData))

	// Parse and optionally store in cache
	var m *twmap.Map
	var parseErr error
	if s.mapCache != nil {
		m, parseErr = s.mapCache.ParseAndPut(info.Name, info.Sha256, mapData)
	} else {
		m, parseErr = twmap.Parse(bytes.NewReader(mapData))
	}
	if parseErr != nil {
		return nil, fmt.Errorf("session06: parse map %q: %w", info.Name, parseErr)
	}

	s.mapMu.Lock()
	s.parsed = m
	s.mapMu.Unlock()

	return m, nil
}

// recvMapDataChunk receives packets until a MAP_DATA system message is found.
// Returns (last, data, error).
func (s *Session) recvMapDataChunk(ctx context.Context) (int, []byte, error) {
	for range 20 {
		hdr, payload, err := s.recvAndParsePayload(ctx)
		if err != nil {
			return 0, nil, err
		}
		if payload == nil {
			continue
		}
		s.ack = packet.CountVitalChunks(payload, hdr.NumChunks, s.ack, Split)

		data := packet.ExtractSysMsgPayload(payload, MsgSysMapData, Split)
		if data == nil {
			continue
		}

		u := packer.NewUnpacker(data)
		last, err := u.NextInt()
		if err != nil {
			return 0, nil, fmt.Errorf("map_data: last: %w", err)
		}
		// crc (skip)
		if _, err := u.NextInt(); err != nil {
			return 0, nil, fmt.Errorf("map_data: crc: %w", err)
		}
		// chunk index (skip)
		if _, err := u.NextInt(); err != nil {
			return 0, nil, fmt.Errorf("map_data: chunk: %w", err)
		}
		chunkSize, err := u.NextInt()
		if err != nil {
			return 0, nil, fmt.Errorf("map_data: chunkSize: %w", err)
		}
		chunkData, err := u.NextRaw(chunkSize)
		if err != nil {
			return 0, nil, fmt.Errorf("map_data: data (%d bytes): %w", chunkSize, err)
		}
		return last, chunkData, nil
	}
	return 0, nil, fmt.Errorf("session06: did not receive MAP_DATA")
}

// SetMap replaces the parsed map (thread-safe). Useful for tests or
// reacting to a mid-session MAP_CHANGE.
func (s *Session) SetMap(m *twmap.Map, info packet.MapInfo) {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	s.mapInfo = info
	s.parsed = m
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

// SendWhisper sends a private message. 0.6 has no native whisper send; DDNet
// accepts the "/whisper <id> <msg>" chat command.
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
