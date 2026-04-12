// Package convert provides bidirectional conversion between Teeworlds/DDNet
// demo files and teehistorian files.
//
// Both conversions are lossy:
//   - Demo → Teehistorian: loses full snapshot state (character positions,
//     velocities, etc.); preserves raw player inputs per tick.
//   - Teehistorian → Demo: loses game messages, console commands, multi-player
//     data; constructs minimal snapshots with just PlayerInput items.
//
// The converters use the shared [replay.InputProvider] interface as a bridge,
// so any format that implements InputProvider can be converted.
//
// Sources:
//   - Teehistorian format: https://ddnet.org/libtw2-doc/teehistorian/
//   - Demo format: https://ddnet.org/libtw2-doc/demo/
//   - DDNet source: src/engine/shared/demo.cpp
package convert

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/replay"
	"github.com/jxsl13/twclient/replay/demo"
	"github.com/jxsl13/twclient/replay/teehistorian"
	"github.com/teeworlds-go/huffman/v2"
	"github.com/teeworlds-go/varint"
)

// --- Teehistorian Writer ---

// ToTeehistorian converts an InputProvider stream into teehistorian binary data.
// The cid parameter specifies the client ID to use in the output.
func ToTeehistorian(src replay.InputProvider, cid int) ([]byte, error) {
	info := src.Info()

	// Header: UUID + JSON + null terminator
	hdr := teehistorian.HeaderJSON{
		Version:    "2",
		GameType:   "ddrace",
		Map:        info.Map,
		ServerName: "replay-convert",
	}
	jsonBytes, err := json.Marshal(hdr)
	if err != nil {
		return nil, fmt.Errorf("convert: marshal teehistorian header: %w", err)
	}

	var buf []byte
	buf = append(buf, teehistorian.HeaderUUID[:]...)
	buf = append(buf, jsonBytes...)
	buf = append(buf, 0) // null terminator

	// JOIN message for the player
	buf = varint.AppendVarint(buf, teehistorian.MsgJoin)
	buf = varint.AppendVarint(buf, cid)

	// PLAYER_NEW with initial position (0,0 — unknown from pure inputs)
	buf = varint.AppendVarint(buf, teehistorian.MsgPlayerNew)
	buf = varint.AppendVarint(buf, cid)
	buf = varint.AppendVarint(buf, 0) // x
	buf = varint.AppendVarint(buf, 0) // y

	var (
		lastTick  = -1
		lastInput [packet.InputFields]int
		hasLast   bool
	)

	for {
		frame, err := src.NextInput()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("convert: read input: %w", err)
		}

		// Emit TICK_SKIP if ticks were skipped.
		// Each INPUT_NEW/INPUT_DIFF implicitly advances the tick by 1.
		// TICK_SKIP(dt) skips dt+1 ticks of silence.
		if lastTick >= 0 && frame.Tick > lastTick+1 {
			skip := frame.Tick - lastTick - 2
			buf = varint.AppendVarint(buf, teehistorian.MsgTickSkip)
			buf = varint.AppendVarint(buf, skip)
		}

		raw := frame.Input.Raw()

		if !hasLast {
			// INPUT_NEW: first input for this CID
			buf = varint.AppendVarint(buf, teehistorian.MsgInputNew)
			buf = varint.AppendVarint(buf, cid)
			for _, v := range raw {
				buf = varint.AppendVarint(buf, v)
			}
			hasLast = true
		} else {
			// INPUT_DIFF: delta from previous input
			buf = varint.AppendVarint(buf, teehistorian.MsgInputDiff)
			buf = varint.AppendVarint(buf, cid)
			for i, v := range raw {
				buf = varint.AppendVarint(buf, v-lastInput[i])
			}
		}

		lastInput = raw
		lastTick = frame.Tick
	}

	// PLAYER_OLD + FINISH
	buf = varint.AppendVarint(buf, teehistorian.MsgPlayerOld)
	buf = varint.AppendVarint(buf, cid)
	buf = varint.AppendVarint(buf, teehistorian.MsgFinish)

	return buf, nil
}

// --- Demo Writer ---

// ToDemo converts an InputProvider stream into demo binary data.
// The cid parameter specifies the client ID for the PlayerInput snapshot items.
func ToDemo(src replay.InputProvider, cid int) ([]byte, error) {
	info := src.Info()

	// Collect all frames first (needed for header length field).
	var frames []replay.InputFrame
	for {
		f, err := src.NextInput()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("convert: read input: %w", err)
		}
		frames = append(frames, f)
	}

	if len(frames) == 0 {
		return nil, fmt.Errorf("convert: no input frames")
	}

	totalTicks := frames[len(frames)-1].Tick - frames[0].Tick + 1

	// --- Build header ---
	var buf []byte

	// Version header (8 bytes)
	buf = append(buf, demo.HeaderMagic[:]...)
	buf = append(buf, demo.VersionTickCompression)

	// Main header (168 bytes)
	buf = append(buf, padString(net6.NetVersion, 64)...)
	buf = append(buf, padString(info.Map, 64)...)
	buf = append(buf, putBE32(0)...) // map_size (no embedded map)
	buf = append(buf, putBE32(0)...) // map_crc
	buf = append(buf, padString("client", 8)...)
	buf = append(buf, putBE32(totalTicks/50)...) // length in seconds (approx)
	ts := time.Now().Format("2006-01-02 15:04:05")
	buf = append(buf, padString(ts, 20)...)

	// Timeline markers (260 bytes for version 5)
	buf = append(buf, putBE32(0)...)                          // num_timeline_markers = 0
	buf = append(buf, make([]byte, demo.MaxTimeMarkers*4)...) // empty markers

	// No map data (map_size = 0)

	// --- Write chunks ---
	lastTick := -1
	for _, f := range frames {
		// Tick marker
		if f.Tick != lastTick {
			buf = append(buf, encodeDemoTick(f.Tick, lastTick)...)
			lastTick = f.Tick
		}

		// Snapshot chunk with PlayerInput item
		snapData := buildPlayerInputSnapshot(cid, f.Input)
		chunkData, err := compressChunk(snapData)
		if err != nil {
			return nil, fmt.Errorf("convert: compress snapshot at tick %d: %w", f.Tick, err)
		}
		buf = append(buf, encodeDemoChunkHeader(demo.ChunkTypeSnapshot, len(chunkData))...)
		buf = append(buf, chunkData...)
	}

	// Emit a trailing tick marker so the demo reader flushes the last
	// pending input (it only delivers on the *next* tick boundary).
	buf = append(buf, encodeDemoTick(lastTick+1, lastTick)...)

	return buf, nil
}

// buildPlayerInputSnapshot constructs a minimal CSnapshot binary blob
// containing a single PlayerInput item.
//
// CSnapshot layout:
//
//	[4] data_size  (int32 LE)
//	[4] num_items  (int32 LE)
//	[4*num_items] offsets (int32 LE)
//	[data area]: item = [4] type_and_id + [40] payload (10 int32 LE fields)
func buildPlayerInputSnapshot(cid int, input packet.PlayerInput) []byte {
	const payloadSize = packet.InputFields * 4 // 40 bytes
	const itemSize = 4 + payloadSize           // TypeAndId + payload
	const numItems = 1
	const dataSize = itemSize

	raw := input.Raw()

	data := make([]byte, 4+4+numItems*4+dataSize)
	binary.LittleEndian.PutUint32(data[0:4], uint32(dataSize)) // data_size
	binary.LittleEndian.PutUint32(data[4:8], uint32(numItems)) // num_items
	binary.LittleEndian.PutUint32(data[8:12], 0)               // offset[0] = 0

	// Data area starts at offset 12
	base := 12
	typeAndID := (demo.SnapTypePlayerInput << 16) | (cid & 0xFFFF)
	binary.LittleEndian.PutUint32(data[base:base+4], uint32(int32(typeAndID)))
	for i, v := range raw {
		binary.LittleEndian.PutUint32(data[base+4+i*4:base+8+i*4], uint32(int32(v)))
	}

	return data
}

// compressChunk applies varint compression then huffman compression
// (reverse of demo.decompressChunk).
func compressChunk(data []byte) ([]byte, error) {
	varintData := packet.VarintCompress(data)
	compressed, err := huffman.Compress(varintData)
	if err != nil {
		return nil, fmt.Errorf("huffman compress: %w", err)
	}
	return compressed, nil
}

// encodeDemoTick encodes a demo tick marker chunk.
// For version 5: uses inline tick delta if possible, else absolute tick.
func encodeDemoTick(tick, prevTick int) []byte {
	if prevTick >= 0 {
		delta := tick - prevTick
		if delta > 0 && delta <= 0x1F { // fits in 5-bit inline field
			return []byte{byte(demo.ChunkTickFlag | demo.ChunkTickInline | delta)}
		}
	}
	// Absolute tick (keyframe)
	var b [5]byte
	b[0] = byte(demo.ChunkTickFlag) // is_tick=1, keyframe=1, inline=0, delta=0
	binary.BigEndian.PutUint32(b[1:5], uint32(tick))
	return b[:]
}

// encodeDemoChunkHeader encodes a demo data chunk header.
// type is 1 (snapshot), 2 (message), or 3 (delta).
func encodeDemoChunkHeader(chunkType, size int) []byte {
	firstByte := byte((chunkType & 0x03) << 5)
	if size < 30 {
		return []byte{firstByte | byte(size)}
	}
	if size <= 255 {
		return []byte{firstByte | 30, byte(size)}
	}
	return []byte{firstByte | 31, byte(size & 0xFF), byte(size >> 8)}
}

// padString creates a fixed-size null-terminated string field.
func padString(s string, size int) []byte {
	b := make([]byte, size)
	copy(b, s)
	return b
}

// putBE32 encodes a signed 32-bit big-endian integer.
func putBE32(v int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(int32(v)))
	return b
}
