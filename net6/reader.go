package net6

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

// reader holds the background reader state for a Session.
// It is embedded in Session and activated by StartReader().
type reader struct {
	eventCh      chan packet.Event
	cancel       context.CancelFunc
	ctx          context.Context
	lastRecv     atomic.Int64
	snaps        *packet.SnapStorage
	snapAssembly *packet.SnapAssemblyState
}

// StartReader launches a background goroutine that reads packets from
// the server, processes chunks, delta-decompresses snapshots, and
// delivers typed events on the Poll channel.
//
// The context governs the reader's lifetime: cancelling it stops the
// reader goroutine. Calling Close also stops the reader.
func (s *Session) StartReader(ctx context.Context) {
	s.reader.ctx, s.reader.cancel = context.WithCancel(ctx)
	s.reader.eventCh = make(chan packet.Event, 128)
	s.reader.snaps = packet.NewSnapStorage(SnapItemSize)
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
		return nil, fmt.Errorf("session06: reader not started")
	}
	ev, ok := <-s.reader.eventCh
	if !ok {
		return nil, fmt.Errorf("session06: reader stopped")
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
			case MsgGameSvDDRaceTimeLegacy:
				s.processDDRaceTime(msgData)
			case MsgGameSvRecordLegacy:
				s.processRecord(msgData)
			}
		}
	}
}

func (s *Session) processSnapSingle(data []byte) {
	u := packer.NewUnpacker(data)
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
	u := packer.NewUnpacker(data)
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
	u := packer.NewUnpacker(data)
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
		asm.Parts[partIndex] = snapData
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
	}
	s.mu.Unlock()
	pkt := append(hdr.Pack(), chunkData...)
	pkt = s.appendSecurityToken(pkt)
	_ = s.conn.SendRaw(pkt)
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

func (s *Session) processDDRaceTime(data []byte) {
	u := packer.NewUnpacker(data)
	timeCentis, err := u.GetInt()
	if err != nil {
		return
	}
	checkCentis, err := u.GetInt()
	if err != nil {
		return
	}
	finishVal, err := u.GetInt()
	if err != nil {
		return
	}
	ev := ddRaceTimeEvent{
		TimeCentis:  timeCentis,
		CheckCentis: checkCentis,
		Finish:      finishVal != 0,
	}
	if ev.Finish || ev.TimeCentis != 0 {
		packet.SendEvent(s.reader.eventCh, ev.ToRaceFinish())
	}
	if ev.CheckCentis != 0 {
		packet.SendEvent(s.reader.eventCh, ev.ToCheckpoint())
	}
}

// ddRaceTimeEvent is the internal 0.6-specific representation of the
// DDRaceTime legacy game message. Use ToRaceFinish/ToCheckpoint to
// convert to the unified packet events.
//
// Refs:
//   - DDNet src/game/client/components/race_demo.cpp
//   - chillerdragon docs: https://chillerdragon.github.io/teeworlds-protocol/06/
type ddRaceTimeEvent struct {
	TimeCentis  int
	CheckCentis int
	Finish      bool
}

func (e ddRaceTimeEvent) ToRaceFinish() packet.EventRaceFinish {
	return packet.EventRaceFinish{
		TimeCentis: e.TimeCentis,
		Finish:     e.Finish,
	}
}

func (e ddRaceTimeEvent) ToCheckpoint() packet.EventCheckpoint {
	return packet.EventCheckpoint{
		DiffCentis: e.CheckCentis,
	}
}

func (s *Session) processRecord(data []byte) {
	u := packer.NewUnpacker(data)
	serverBest, err := u.GetInt()
	if err != nil {
		return
	}
	playerBest, err := u.GetInt()
	if err != nil {
		return
	}
	packet.SendEvent(s.reader.eventCh, packet.EventRecord{
		ServerBestCentis: serverBest,
		PlayerBestCentis: playerBest,
	})
}
