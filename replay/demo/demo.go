// Package demo parses Teeworlds/DDNet demo files (.demo) which record
// snapshots and messages from either client or server perspective.
//
// For training, we extract PlayerInput objects from snapshots. In a client
// demo, only the local player's input is available. In a server demo, a
// specific client ID must be selected.
package demo

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/replay"
	"github.com/teeworlds-go/huffman/v2"
)

const (
	MaxChunkSize   = 64 * 1024
	MaxTimeMarkers = 64
	maxSnapItems   = 1024

	ChunkTickFlag       = 0x80
	ChunkTickInline     = 0x20
	chunkMaskTick       = 0x1f
	chunkMaskTickLegacy = 0x3f
	chunkMaskType       = 0x60
	chunkMaskSize       = 0x1f

	ChunkTypeSnapshot = 1
	ChunkTypeMessage  = 2
	ChunkTypeDelta    = 3

	VersionCurrent         = 6
	VersionOld             = 3
	versionSha256          = 6
	VersionTickCompression = 5

	// Snapshot item type IDs (DDNet 0.6 protocol, from datasrc/network.py)
	SnapTypePlayerInput = 1
	SnapTypeCharacter   = 9
	SnapTypePlayerInfo  = 10
	SnapTypeClientInfo  = 11

	// PlayerInfo field offsets (int32 indices after TypeAndId)
	playerInfoLocal    = 0 // m_Local: 1 = this is the recording player
	playerInfoClientID = 1 // m_ClientId
)

// HeaderMagic is the 7-byte file signature for Teeworlds/DDNet demo files.
var HeaderMagic = [7]byte{'T', 'W', 'D', 'E', 'M', 'O', 0}

var sha256ExtUUID = [16]byte{
	0x6b, 0xe6, 0xda, 0x4a, 0xce, 0xbd, 0x38, 0x0c,
	0x9b, 0x5b, 0x12, 0x89, 0xc8, 0x42, 0xd7, 0x80,
}

// Header is the on-disk demo file header.
type Header struct {
	Marker    [7]byte
	Version   uint8
	Netver    [64]byte
	MapName   [64]byte
	MapSize   [4]byte
	MapCRC    [4]byte
	Type      [8]byte
	Length    [4]byte
	Timestamp [20]byte
}

// Loader reads demo files and provides input frames or character frames
// from snapshots. Client demos contain PlayerInput items and support
// NextInput() directly. Server demos (DDNet) contain only Character items;
// use NextCharacter() and wrap with replay.CharacterToInputAdapter to
// derive inputs.
type Loader struct {
	file    *os.File
	hdr     Header
	info    replay.RecordingInfo
	version int
	cid     int // selected CID; -1 means auto-detect

	tick           int
	hasInput       bool
	input          packet.PlayerInput
	hasCharacter   bool
	character      replay.CharacterFrame
	cidDetected    bool                // true once CID has been resolved from a snapshot
	hasPlayerInput bool                // true if snapshots contain PlayerInput items (client demo)
	snaps          *packet.SnapStorage // maintains snapshot state for delta decompression
	lastSnapTick   int                 // tick of last stored snapshot (-1 = none)

	done bool
}

// Open opens a demo file and reads the header.
func Open(filename string, cid int) (*Loader, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("demo: open %s: %w", filename, err)
	}

	l := &Loader{
		file:         f,
		cid:          cid,
		tick:         -1,
		snaps:        packet.NewSnapStorage(net6.SnapItemSize),
		lastSnapTick: -1,
	}

	if err := l.readHeader(); err != nil {
		f.Close()
		return nil, err
	}

	return l, nil
}

func (l *Loader) readHeader() error {
	if err := binary.Read(l.file, binary.BigEndian, &l.hdr); err != nil {
		return fmt.Errorf("demo: read header: %w", err)
	}
	if l.hdr.Marker != HeaderMagic {
		return fmt.Errorf("demo: invalid magic")
	}
	l.version = int(l.hdr.Version)
	if l.version < VersionOld || l.version > VersionCurrent {
		return fmt.Errorf("demo: unsupported version %d", l.version)
	}

	// Read timeline markers for version > 3
	if l.version > VersionOld {
		var numMarkers [4]byte
		if _, err := io.ReadFull(l.file, numMarkers[:]); err != nil {
			return fmt.Errorf("demo: read timeline marker count: %w", err)
		}
		markerData := make([]byte, MaxTimeMarkers*4)
		if _, err := io.ReadFull(l.file, markerData); err != nil {
			return fmt.Errorf("demo: read timeline markers: %w", err)
		}
	}

	// SHA256 extension for version >= 6
	if l.version >= versionSha256 {
		var extUUID [16]byte
		if _, err := io.ReadFull(l.file, extUUID[:]); err != nil {
			return fmt.Errorf("demo: read sha256 ext: %w", err)
		}
		if extUUID == sha256ExtUUID {
			var sha256 [32]byte
			if _, err := io.ReadFull(l.file, sha256[:]); err != nil {
				return fmt.Errorf("demo: read sha256: %w", err)
			}
		} else {
			if _, err := l.file.Seek(-16, io.SeekCurrent); err != nil {
				return fmt.Errorf("demo: seek back: %w", err)
			}
		}
	}

	// Skip map data
	mapSize := binary.BigEndian.Uint32(l.hdr.MapSize[:])
	if mapSize > 0 {
		if _, err := l.file.Seek(int64(mapSize), io.SeekCurrent); err != nil {
			return fmt.Errorf("demo: skip map data: %w", err)
		}
	}

	// For client demos, the local CID is auto-detected via the m_Local
	// flag in PlayerInfo snapshot items. For server demos without an
	// explicit CID, the smallest CID with a PlayerInput item is chosen.
	l.info = replay.RecordingInfo{
		Format:      replay.FormatDemo,
		Map:         packet.CString(l.hdr.MapName[:]),
		SelectedCID: l.cid,
	}

	return nil
}

// NextInput reads chunks until we find a snapshot with player input at a new tick.
// Only works for client demos that contain PlayerInput snapshot items.
// For server demos (no PlayerInput), use NextCharacter() with CharacterToInputAdapter.
func (l *Loader) NextInput() (replay.InputFrame, error) {
	if l.done {
		return replay.InputFrame{}, io.EOF
	}

	for {
		chunkByte, chunkSize, err := l.readChunkHeader()
		if err != nil {
			l.done = true
			if l.hasInput && l.tick >= 0 {
				frame := replay.InputFrame{Tick: l.tick, Input: l.input}
				l.hasInput = false
				return frame, nil
			}
			return replay.InputFrame{}, io.EOF
		}

		if chunkByte&ChunkTickFlag != 0 {
			// Deliver any pending input from previous tick
			if l.hasInput && l.tick >= 0 {
				frame := replay.InputFrame{
					Tick:  l.tick,
					Input: l.input,
				}
				l.hasInput = false
				return frame, nil
			}
			continue
		}

		if chunkSize == 0 {
			continue
		}
		if chunkSize > MaxChunkSize {
			l.done = true
			return replay.InputFrame{}, fmt.Errorf("demo: chunk too large: %d", chunkSize)
		}

		compressed := make([]byte, chunkSize)
		if _, err := io.ReadFull(l.file, compressed); err != nil {
			l.done = true
			return replay.InputFrame{}, io.EOF
		}

		actualType := (chunkByte & chunkMaskType) >> 5
		if actualType == ChunkTypeSnapshot || actualType == ChunkTypeDelta {
			l.processSnap(compressed, actualType == ChunkTypeSnapshot)
		}
	}
}

// NextCharacter reads chunks until we find a snapshot with character state at a new tick.
// Works for both client and server demos — Character items (type 9, CNetObj_Character)
// are present in all demo types.
func (l *Loader) NextCharacter() (replay.CharacterFrame, error) {
	if l.done {
		return replay.CharacterFrame{}, io.EOF
	}

	for {
		chunkByte, chunkSize, err := l.readChunkHeader()
		if err != nil {
			l.done = true
			if l.hasCharacter && l.tick >= 0 {
				frame := l.character
				l.hasCharacter = false
				return frame, nil
			}
			return replay.CharacterFrame{}, io.EOF
		}

		if chunkByte&ChunkTickFlag != 0 {
			if l.hasCharacter && l.tick >= 0 {
				frame := l.character
				l.hasCharacter = false
				return frame, nil
			}
			continue
		}

		if chunkSize == 0 {
			continue
		}
		if chunkSize > MaxChunkSize {
			l.done = true
			return replay.CharacterFrame{}, fmt.Errorf("demo: chunk too large: %d", chunkSize)
		}

		compressed := make([]byte, chunkSize)
		if _, err := io.ReadFull(l.file, compressed); err != nil {
			l.done = true
			return replay.CharacterFrame{}, io.EOF
		}

		actualType := (chunkByte & chunkMaskType) >> 5
		if actualType == ChunkTypeSnapshot || actualType == ChunkTypeDelta {
			l.processSnap(compressed, actualType == ChunkTypeSnapshot)
		}
	}
}

// processSnap decompresses a snapshot or delta chunk and extracts player input.
//
// For full snapshots (keyframes): huffman+varint decompress → CSnapshot binary
// → convert to packet.Snapshot and store as base for future deltas.
//
// For delta snapshots: huffman-only decompress → varint-encoded delta data →
// apply against last snapshot via packet.SnapStorage.ProcessSnap (reuses the
// same applyDelta logic as the live protocol in net6/reader.go).
//
// Sources:
//   - DDNet demo.cpp CDemoRecorder::RecordSnapshot / CDemoPlayer::DoTick
//   - DDNet snapshot.cpp CSnapshotDelta::UnpackDelta
//   - packet/snap.go applyDelta (varint-encoded delta format)
func (l *Loader) processSnap(compressed []byte, isFullSnap bool) {
	var snap *packet.Snapshot

	if isFullSnap {
		// Full snapshot: decompress fully (huffman + varint → int32 LE),
		// then convert the CSnapshot binary to packet.Snapshot.
		data, err := decompressChunk(compressed)
		if err != nil {
			l.hasInput = true
			return
		}
		snap = rawSnapshotToPacket(l.tick, data)
		l.snaps.Snaps[l.tick] = snap
		l.snaps.LastTick = l.tick
		l.lastSnapTick = l.tick
	} else {
		// Delta snapshot: huffman-only decompress to get varint-encoded
		// delta data, then apply via ProcessSnap against the last snapshot.
		varintData, err := huffman.Decompress(compressed)
		if err != nil {
			l.hasInput = true
			return
		}
		if l.lastSnapTick < 0 {
			// No base snapshot yet — can't apply delta.
			l.hasInput = true
			return
		}
		deltaTick := l.tick - l.lastSnapTick
		snap, err = l.snaps.ProcessSnap(l.tick, deltaTick, varintData)
		if err != nil {
			l.hasInput = true
			return
		}
		l.lastSnapTick = l.tick
	}

	l.extractInputFromSnap(snap)
}

// extractInputFromSnap performs CID auto-detection and extracts PlayerInput
// and Character items from a decoded packet.Snapshot.
func (l *Loader) extractInputFromSnap(snap *packet.Snapshot) {
	if !l.cidDetected {
		// Strategy 1: look for PlayerInfo with m_Local == 1 (client demos)
		for _, it := range snap.Items {
			if it.TypeID == SnapTypePlayerInfo && len(it.Fields) >= 2 {
				if it.Fields[playerInfoLocal] == 1 {
					l.cid = it.Fields[playerInfoClientID]
					l.info.SelectedCID = l.cid
					l.cidDetected = true
					break
				}
			}
		}
		// Strategy 2: pick smallest PlayerInput CID (client demo)
		if !l.cidDetected && l.cid < 0 {
			smallest := -1
			for _, it := range snap.Items {
				if it.TypeID == SnapTypePlayerInput {
					if smallest < 0 || it.ID < smallest {
						smallest = it.ID
					}
				}
			}
			if smallest >= 0 {
				l.cid = smallest
				l.info.SelectedCID = smallest
				l.hasPlayerInput = true
			}
		}
		// Strategy 3: pick smallest Character CID (server demo — no PlayerInput)
		if !l.cidDetected && l.cid < 0 {
			smallest := -1
			for _, it := range snap.Items {
				if it.TypeID == SnapTypeCharacter {
					if smallest < 0 || it.ID < smallest {
						smallest = it.ID
					}
				}
			}
			if smallest >= 0 {
				l.cid = smallest
				l.info.SelectedCID = smallest
			}
		}
		l.cidDetected = true
	}

	// Extract PlayerInput for the selected CID (client demos).
	for _, it := range snap.Items {
		if it.TypeID == SnapTypePlayerInput && it.ID == l.cid && len(it.Fields) >= packet.InputFields {
			var raw [10]int
			copy(raw[:], it.Fields[:packet.InputFields])
			l.input = packet.UnsafePlayerInputFromRaw(raw)
			l.hasInput = true
			l.hasPlayerInput = true
		}
	}

	// Extract Character for the selected CID (all demo types).
	for _, it := range snap.Items {
		if it.TypeID == SnapTypeCharacter && it.ID == l.cid && len(it.Fields) >= net6.SizeCharacter {
			l.character = characterFromSnap(l.tick, it.Fields)
			l.hasCharacter = true
			// For server demos (no PlayerInput), also set hasInput so
			// NextInput delivers a (zero) frame for tick-counting.
			if !l.hasPlayerInput {
				l.hasInput = true
			}
		}
	}

	// If neither was found, still mark tick as having data.
	if !l.hasInput && !l.hasCharacter {
		l.hasInput = true
	}
}

// characterFromSnap converts CNetObj_Character fields (22 int) to a CharacterFrame.
//
// Field layout (from DDNet datasrc/network.py, CNetObj_Character):
//
//	[0]=Tick  [1]=X  [2]=Y  [3]=VelX  [4]=VelY  [5]=Angle  [6]=Direction
//	[7]=Jumped  [8]=HookedPlayer  [9]=HookState  [10]=HookTick
//	[11]=HookX  [12]=HookY  [13]=HookDx  [14]=HookDy
//	[15]=PlayerFlags  [16]=Health  [17]=Armor
//	[18]=AmmoCount  [19]=Weapon  [20]=Emote  [21]=AttackTick
//
// Sources:
//   - DDNet datasrc/network.py CNetObj_Character
//   - client/snap.go characterFromFields
//   - replay/ghost/ghost.go parseCharacter
func characterFromSnap(tick int, fields []int) replay.CharacterFrame {
	return replay.CharacterFrame{
		Tick:       tick,
		X:          fields[1],
		Y:          fields[2],
		VelX:       fields[3],
		VelY:       fields[4],
		Angle:      fields[5],
		Direction:  fields[6],
		Weapon:     replay.CharWeapon(fields[19]),
		HookState:  replay.CharHookState(fields[9]),
		HookX:      fields[11],
		HookY:      fields[12],
		AttackTick: fields[21],
	}
}

// rawSnapshotToPacket converts a decompressed CSnapshot binary (int32 LE array)
// into a packet.Snapshot with typed items and integer fields.
func rawSnapshotToPacket(tick int, data []byte) *packet.Snapshot {
	snap := &packet.Snapshot{Tick: tick}
	items := parseSnapshotItems(data)
	for _, it := range items {
		nFields := len(it.data) / 4
		fields := make([]int, nFields)
		for i := range nFields {
			fields[i] = packet.ReadInt32LE(it.data[i*4 : i*4+4])
		}
		snap.Items = append(snap.Items, packet.SnapItem{
			TypeID: it.itemType,
			ID:     it.itemID,
			Fields: fields,
		})
	}
	return snap
}

// snapItem represents a single item parsed from a CSnapshot.
type snapItem struct {
	itemType int
	itemID   int
	data     []byte // item payload (after TypeAndId)
}

// parseSnapshotItems extracts items from a decompressed full CSnapshot.
//
// Binary layout after varint+huffman decompression:
//
//	[0:4]  m_DataSize  (int32 LE) – size of data area in bytes
//	[4:8]  m_NumItems  (int32 LE)
//	[8:]   offsets[m_NumItems] (int32 LE each) – byte offset into data area
//	[8+numItems*4:]  data area – items as CSnapshotItem (TypeAndId + payload)
func parseSnapshotItems(data []byte) []snapItem {
	if len(data) < 8 {
		return nil
	}
	dataSize := int(int32(binary.LittleEndian.Uint32(data[0:4])))
	numItems := int(int32(binary.LittleEndian.Uint32(data[4:8])))
	if numItems <= 0 || numItems > maxSnapItems || dataSize <= 0 {
		return nil
	}

	offsetsEnd := 8 + numItems*4
	dataAreaStart := offsetsEnd
	if dataAreaStart+dataSize > len(data) {
		return nil
	}

	items := make([]snapItem, 0, numItems)
	for i := range numItems {
		off := int(int32(binary.LittleEndian.Uint32(data[8+i*4 : 8+i*4+4])))
		abs := dataAreaStart + off
		if abs+4 > len(data) {
			break
		}

		// Determine item end
		var absEnd int
		if i+1 < numItems {
			nextOff := int(int32(binary.LittleEndian.Uint32(data[8+(i+1)*4 : 8+(i+1)*4+4])))
			absEnd = dataAreaStart + nextOff
		} else {
			absEnd = dataAreaStart + dataSize
		}
		if absEnd > len(data) || absEnd <= abs+4 {
			break
		}

		typeAndID := int(int32(binary.LittleEndian.Uint32(data[abs : abs+4])))
		items = append(items, snapItem{
			itemType: (typeAndID >> 16) & 0xFFFF,
			itemID:   typeAndID & 0xFFFF,
			data:     data[abs+4 : absEnd],
		})
	}
	return items
}

func (l *Loader) readChunkHeader() (int, int, error) {
	var chunk [1]byte
	if _, err := io.ReadFull(l.file, chunk[:]); err != nil {
		return 0, 0, err
	}

	b := int(chunk[0])

	if b&ChunkTickFlag != 0 {
		if b&ChunkTickInline != 0 && l.version >= VersionTickCompression {
			delta := b & chunkMaskTick
			if l.tick < 0 {
				return 0, 0, fmt.Errorf("demo: tick delta before first tick")
			}
			l.tick += delta
		} else if l.version < VersionTickCompression && (b&chunkMaskTickLegacy) != 0 {
			delta := b & chunkMaskTickLegacy
			if l.tick < 0 {
				return 0, 0, fmt.Errorf("demo: legacy tick delta before first tick")
			}
			l.tick += delta
		} else {
			var tickData [4]byte
			if _, err := io.ReadFull(l.file, tickData[:]); err != nil {
				return 0, 0, err
			}
			l.tick = int(binary.BigEndian.Uint32(tickData[:]))
		}
		return b, 0, nil
	}

	size := b & chunkMaskSize
	switch size {
	case 30:
		var sd [1]byte
		if _, err := io.ReadFull(l.file, sd[:]); err != nil {
			return 0, 0, err
		}
		size = int(sd[0])
	case 31:
		var sd [2]byte
		if _, err := io.ReadFull(l.file, sd[:]); err != nil {
			return 0, 0, err
		}
		size = int(sd[0]) | (int(sd[1]) << 8)
	}

	return b, size, nil
}

// Info returns metadata about the demo recording.
func (l *Loader) Info() replay.RecordingInfo {
	return l.info
}

// HasPlayerInput reports whether the demo contains PlayerInput snapshot items.
// Client demos have PlayerInput; DDNet server demos typically only have
// Character items. Call this after at least one NextInput/NextCharacter call
// to ensure CID detection has run.
func (l *Loader) HasPlayerInput() bool {
	return l.hasPlayerInput
}

// Close releases the demo file.
func (l *Loader) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func decompressChunk(compressed []byte) ([]byte, error) {
	decompressed, err := huffman.Decompress(compressed)
	if err != nil {
		return nil, fmt.Errorf("demo: huffman decompress: %w", err)
	}

	result := make([]byte, MaxChunkSize)
	finalSize := packet.VarintDecompress(decompressed, result)
	if finalSize < 0 {
		return nil, fmt.Errorf("demo: varint decompress failed")
	}

	return result[:finalSize], nil
}
