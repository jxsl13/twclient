package net7

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// readerTimeout is the short read timeout used by the background reader
// so it can periodically check the stop signal.
const readerTimeout = 50 * time.Millisecond

// defaultEventChanSize is the reader event-channel buffer used when none is
// configured (V54).
const defaultEventChanSize = 128

// eventChanSizeOrDefault returns n if positive, else the default (V54).
func eventChanSizeOrDefault(n int) int {
	if n <= 0 {
		return defaultEventChanSize
	}
	return n
}

// reader holds the background reader state for a Session.
// It is embedded in Session and activated by StartReader().
type reader struct {
	eventCh      chan packet.Event
	cancel       context.CancelFunc
	ctx          context.Context
	lastRecv     atomic.Int64
	snaps        *packet.SnapStorage
	snapAssembly *packet.SnapAssemblyState
	// snapUnpacker is reused across the 50Hz snapshot parse path so each
	// inbound snap message no longer allocates a fresh Unpacker (T37, V51).
	// Goroutine-confined to readLoop; never used for data retained past the
	// call (multipart parts are copied out, V52).
	snapUnpacker packer.Unpacker
}

// StartReader launches a background goroutine that reads packets from
// the server, processes chunks, delta-decompresses snapshots, and
// delivers typed events on the Poll channel.
//
// The context governs the reader's lifetime: cancelling it stops the
// reader goroutine. Calling Close also stops the reader.
func (s *Session) StartReader(ctx context.Context) {
	s.reader.ctx, s.reader.cancel = context.WithCancel(ctx)
	s.reader.eventCh = make(chan packet.Event, eventChanSizeOrDefault(s.eventChanSize))
	s.reader.snaps = packet.NewSnapStorage(nil, packet.WithMaxSnaps(s.snapStorageSize))
	s.reader.lastRecv.Store(time.Now().UnixNano())

	go s.readLoop()
}

// EventCh returns the event channel for non-blocking select.
func (s *Session) EventCh() <-chan packet.Event {
	return s.reader.eventCh
}

// Poll returns the next event from the session. Blocks until an event
// is available. Returns (nil, error) on disconnect or after Close.
func (s *Session) Poll() (packet.Event, error) {
	if s.reader.eventCh == nil {
		return nil, fmt.Errorf("session07: reader not started")
	}
	ev, ok := <-s.reader.eventCh
	if !ok {
		return nil, fmt.Errorf("session07: reader stopped")
	}
	return ev, nil
}

// StopReader signals the background reader to stop.
func (s *Session) StopReader() {
	if s.reader.cancel != nil {
		s.reader.cancel()
		s.reader.cancel = nil
	}
}

// LastRecvTime returns the wall-clock time of the last successful recv.
func (s *Session) LastRecvTime() time.Time {
	return time.Unix(0, s.reader.lastRecv.Load())
}

// LastSnapTick returns the tick of the most recent snapshot.
func (s *Session) LastSnapTick() int {
	if s.reader.snaps == nil {
		return 0
	}
	return s.reader.snaps.LastTick
}

func (s *Session) readLoop() {
	defer close(s.reader.eventCh)

	ctx := s.reader.ctx
	const keepaliveInterval = 2 * time.Second
	const reackInterval = 500 * time.Millisecond
	lastSend := time.Now()
	lastReack := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Automatic keepalive: send if no packet was sent recently.
		if time.Since(lastSend) > keepaliveInterval {
			_ = s.SendKeepAlive()
			lastSend = time.Now()
		}

		// Periodic re-ack: resend ack for the latest snapshot tick.
		// Protects against a single lost ack causing server to degrade
		// to SNAPRATE_RECOVER.
		if time.Since(lastReack) > reackInterval {
			if tick := int(s.lastAckedSnap.Load()); tick > 0 {
				s.forceAckSnap(tick)
			}
			lastReack = time.Now()
		}

		hdr, payload, err := s.recvAndAckTimeout(ctx, readerTimeout)
		if err != nil {
			if packet.IsTimeout(err) {
				continue
			}
			// Fatal I/O error — deliver close event
			packet.SendEvent(s.reader.eventCh, packet.EventClose{Reason: err.Error()})
			return
		}

		s.reader.lastRecv.Store(time.Now().UnixNano())
		lastSend = time.Now() // any recv implies ack was sent

		// Server CLOSE control packet
		if hdr.Flags.Control && len(payload) > 0 && payload[0] == MsgCtrlClose {
			reason := ""
			if len(payload) > 1 {
				reason = string(payload[1:])
			}
			packet.SendEvent(s.reader.eventCh, packet.EventClose{Reason: reason})
			return
		}

		if payload != nil {
			s.processPayload(payload)
		}
	}
}

func (s *Session) processPayload(payload []byte) {
	chunks := packet.UnpackChunks(payload, Split)
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
		msgData := ch.Data[1:]

		if sys {
			switch msgID {
			case MsgSysSnapSingle:
				s.processSnapSingle(msgData)
			case MsgSysSnapEmpty:
				s.processSnapEmpty(msgData)
			case MsgSysSnap:
				s.processSnapMulti(msgData)
			case MsgSysPing:
				_ = s.SendVitalMsg(SysPingReply())
			case MsgSysInputTiming:
				s.processInputTiming(msgData)
			case MsgSysMapChange:
				info, err := packet.ParseMapChangePayload(msgData)
				if err == nil {
					s.mapMu.Lock()
					s.mapInfo = info
					s.parsed = nil
					s.mapMu.Unlock()
					packet.SendEvent(s.reader.eventCh, packet.EventMapChange{Info: info})
				}
			}
		} else {
			switch msgID {
			case MsgGameSvMotd:
				s.processMotd(msgData)
			case MsgGameSvBroadcast:
				s.processBroadcast(msgData)
			case MsgGameSvChat:
				s.processChat(msgData)
			case MsgGameSvTeam:
				s.processTeam(msgData)
			case MsgGameSvKillMsg:
				s.processKillMsg(msgData)
			case MsgGameSvTuneParams:
				s.processTuneParams(msgData)
			case MsgGameSvWeaponPickup:
				s.processWeaponPickup(msgData)
			case MsgGameSvEmoticon:
				s.processEmoticon(msgData)
			case MsgGameSvVoteSet:
				s.processVoteSet(msgData)
			case MsgGameSvVoteStatus:
				s.processVoteStatus(msgData)
			case MsgGameSvVoteOptionAdd:
				s.processVoteOptionAdd(msgData)
			case MsgGameSvVoteOptionRemove:
				s.processVoteOptionRemove(msgData)
			case MsgGameSvVoteClearOptions:
				s.processVoteClearOptions()
			case MsgGameSvServerSettings:
				s.processServerSettings(msgData)
			case MsgGameSvClientInfo:
				s.processClientInfo(msgData)
			case MsgGameSvClientDrop:
				s.processClientDrop(msgData)
			case MsgGameSvGameInfo:
				s.processGameInfo(msgData)
			case MsgGameSvGameMsg:
				s.processGameMsg(msgData)
			case MsgGameSvSkinChange:
				s.processSkinChange(msgData)
			case MsgGameSvCommandInfo:
				s.processCommandInfo(msgData)
			case MsgGameSvCommandInfoRemove:
				s.processCommandInfoRemove(msgData)
			case MsgGameSvRaceFinish:
				s.processRaceFinish(msgData)
			case MsgGameSvCheckpoint:
				s.processCheckpoint(msgData)
			}
		}
	}
}

func (s *Session) processSnapSingle(data []byte) {
	u := &s.reader.snapUnpacker
	u.Reset(data)
	tick, err := u.GetInt()
	if err != nil {
		return
	}
	deltaTick, err := u.GetInt()
	if err != nil {
		return
	}
	if _, err := u.GetInt(); err != nil {
		return // CRC
	}
	partSize, err := u.GetInt()
	if err != nil {
		return
	}
	snapData, err := u.GetRaw(partSize)
	if err != nil {
		return
	}

	snap, err := s.reader.snaps.ProcessSnap(tick, deltaTick, snapData)
	if err != nil {
		s.log.Debug("snap process error", "error", err, "tick", tick)
		return
	}
	s.ackSnap(tick)
	packet.SendEvent(s.reader.eventCh, packet.EventSnapshot{Snap: snap})
}

func (s *Session) processSnapEmpty(data []byte) {
	u := &s.reader.snapUnpacker
	u.Reset(data)
	tick, err := u.GetInt()
	if err != nil {
		return
	}
	deltaTick, err := u.GetInt()
	if err != nil {
		return
	}
	snap, err := s.reader.snaps.ProcessSnap(tick, deltaTick, nil)
	if err != nil {
		s.log.Debug("snap empty process error", "error", err, "tick", tick)
		return
	}
	s.ackSnap(tick)
	packet.SendEvent(s.reader.eventCh, packet.EventSnapshot{Snap: snap})
}

func (s *Session) processSnapMulti(data []byte) {
	u := &s.reader.snapUnpacker
	u.Reset(data)
	tick, err := u.GetInt()
	if err != nil {
		return
	}
	deltaTick, err := u.GetInt()
	if err != nil {
		return
	}
	numParts, err := u.GetInt()
	if err != nil {
		return
	}
	partIndex, err := u.GetInt()
	if err != nil {
		return
	}
	if _, err := u.GetInt(); err != nil {
		return // CRC
	}
	partSize, err := u.GetInt()
	if err != nil {
		return
	}
	snapData, err := u.GetRaw(partSize)
	if err != nil {
		return
	}

	if numParts == 1 {
		snap, err := s.reader.snaps.ProcessSnap(tick, deltaTick, snapData)
		if err != nil {
			s.log.Debug("snap multi process error", "error", err, "tick", tick)
			return
		}
		s.ackSnap(tick)
		packet.SendEvent(s.reader.eventCh, packet.EventSnapshot{Snap: snap})
		return
	}

	asm := s.reader.snapAssembly
	if asm == nil || asm.Tick != tick || asm.NumParts != numParts {
		asm = &packet.SnapAssemblyState{
			Tick:      tick,
			DeltaTick: deltaTick,
			NumParts:  numParts,
			Parts:     make([][]byte, numParts),
		}
		s.reader.snapAssembly = asm
	}
	if partIndex >= 0 && partIndex < numParts {
		// snapData aliases the reused snapUnpacker buffer; the part is retained
		// across messages until all parts arrive, so copy it out (V52).
		asm.Parts[partIndex] = append([]byte(nil), snapData...)
		asm.Received++
	}

	if asm.Received == numParts {
		var full []byte
		for _, p := range asm.Parts {
			full = append(full, p...)
		}
		snap, err := s.reader.snaps.ProcessSnap(tick, deltaTick, full)
		if err != nil {
			s.log.Debug("snap multi assembled process error", "error", err, "tick", tick)
			return
		}
		s.ackSnap(tick)
		packet.SendEvent(s.reader.eventCh, packet.EventSnapshot{Snap: snap})
		s.reader.snapAssembly = nil
	}
}

func (s *Session) processRaceFinish(data []byte) {
	u := packer.NewUnpacker(data)
	timeCentis, err := u.GetInt()
	if err != nil {
		return
	}
	// diff (unused for now)
	if _, err := u.GetInt(); err != nil {
		return
	}
	// player id (unused for now)
	if _, err := u.GetInt(); err != nil {
		return
	}
	ev := raceFinishEvent{TimeCentis: timeCentis}
	packet.SendEvent(s.reader.eventCh, ev.ToRaceFinish())
}

func (s *Session) processCheckpoint(data []byte) {
	u := packer.NewUnpacker(data)
	diff, err := u.GetInt()
	if err != nil {
		return
	}
	ev := checkpointEvent{DiffCentis: diff}
	packet.SendEvent(s.reader.eventCh, ev.ToCheckpoint())
}

// raceFinishEvent is the internal 0.7-specific representation of the
// SV_RACE_FINISH game message.
//
// Refs:
//   - DDNet src/engine/shared/protocol7.h (NETMSG_SV_RACE_FINISH)
type raceFinishEvent struct {
	TimeCentis int
}

func (e raceFinishEvent) ToRaceFinish() packet.EventRaceFinish {
	return packet.EventRaceFinish{
		TimeCentis: e.TimeCentis,
		Finish:     true,
	}
}

// checkpointEvent is the internal 0.7-specific representation of the
// SV_CHECKPOINT game message.
//
// Refs:
//   - DDNet src/engine/shared/protocol7.h (NETMSG_SV_CHECKPOINT)
type checkpointEvent struct {
	DiffCentis int
}

func (e checkpointEvent) ToCheckpoint() packet.EventCheckpoint {
	return packet.EventCheckpoint{
		DiffCentis: e.DiffCentis,
	}
}

func (s *Session) processInputTiming(data []byte) {
	u := packer.NewUnpacker(data)
	intendedTick, err := u.GetInt()
	if err != nil {
		return
	}
	timeLeft, err := u.GetInt()
	if err != nil {
		return
	}
	packet.SendEvent(s.reader.eventCh, packet.EventInputTiming{
		IntendedTick: intendedTick,
		TimeLeft:     timeLeft,
	})
}

// ackSnap sends a minimal INPUT message to acknowledge a snapshot tick.
// Called from the reader goroutine immediately after processing a snap so
// the server's LastAckedSnapshot stays fresh and it doesn't degrade to
// SNAPRATE_RECOVER (1 snap/sec).
func (s *Session) ackSnap(tick int) {
	// Only ack forward — never let the server's LastAckedSnapshot go backwards.
	for {
		old := s.lastAckedSnap.Load()
		if int64(tick) <= old {
			return // already acked a newer or equal tick
		}
		if s.lastAckedSnap.CompareAndSwap(old, int64(tick)) {
			break
		}
	}
	s.sendAckPacket(tick)
}

// forceAckSnap re-sends an ack for the given tick unconditionally.
// Used for periodic re-acks to recover from lost UDP ack packets.
func (s *Session) forceAckSnap(tick int) {
	s.sendAckPacket(tick)
}

func (s *Session) sendAckPacket(tick int) {
	inputMsg := SysInput(tick, tick+1, packet.EmptyInputSize, packet.EmptyInput)
	chunkData := WrapChunk(inputMsg)
	s.mu.Lock()
	hdr := Header{
		Ack:       s.ack,
		NumChunks: 1,
		Token:     s.serverToken,
	}
	s.mu.Unlock()
	pkt := append(hdr.Pack(), chunkData...)
	_ = s.conn.SendRaw(pkt)
}
