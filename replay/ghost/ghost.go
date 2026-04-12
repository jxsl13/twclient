// Package ghost parses DDNet ghost files (.gho) which record character
// positions for race ghost replay.
//
// Ghost files contain character state (position, velocity, weapon, etc.)
// rather than raw player inputs. Use replay.NewCharacterToInputAdapter
// to derive approximate inputs from successive frames.
package ghost

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/replay"
	"github.com/teeworlds-go/huffman/v2"
)

// Ghost file constants matching DDNet engine/client/ghost.h
const (
	maxItemSize      = 128
	numItemsPerChunk = 50
	maxChunkSize     = maxItemSize * numItemsPerChunk // 6400

	// Ghost data types
	ghostDataTypeSkin            = 0
	ghostDataTypeCharacterNoTick = 1
	ghostDataTypeCharacter       = 2
	ghostDataTypeStartTick       = 3

	// Character struct sizes (in bytes, all fields are int32)
	characterNoTickSize = 11 * 4 // 44 bytes
	characterSize       = 12 * 4 // 48 bytes (with Tick)
	skinSize            = 9 * 4  // 36 bytes
	startTickSize       = 4      // single int32
)

// HeaderMagic is the 8-byte file signature for DDNet ghost files.
var HeaderMagic = [8]byte{'T', 'W', 'G', 'H', 'O', 'S', 'T', 0}

// Loader reads ghost files and provides character frames.
type Loader struct {
	file    *os.File
	info    replay.RecordingInfo
	version int

	// chunk state
	buffer      []byte
	bufferPos   int
	bufferEnd   int
	numItems    int
	currentItem int
	prevItem    int // last item index returned by readNextType
	lastType    int
	lastData    [maxItemSize]byte

	// frame tracking
	startTick int
	frameIdx  int

	done bool
}

// Open opens a ghost file and reads the header.
func Open(filename string) (*Loader, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("ghost: open %s: %w", filename, err)
	}

	l := &Loader{
		file:      f,
		lastType:  -1,
		startTick: -1,
	}

	if err := l.readHeader(); err != nil {
		f.Close()
		return nil, err
	}

	return l, nil
}

func (l *Loader) readHeader() error {
	var magic [8]byte
	if err := binary.Read(l.file, binary.BigEndian, &magic); err != nil {
		return fmt.Errorf("ghost: read magic: %w", err)
	}
	if magic != HeaderMagic {
		return fmt.Errorf("ghost: invalid magic %x", magic)
	}

	var version uint8
	if err := binary.Read(l.file, binary.BigEndian, &version); err != nil {
		return fmt.Errorf("ghost: read version: %w", err)
	}
	l.version = int(version)
	if l.version < 4 || l.version > 6 {
		return fmt.Errorf("ghost: unsupported version %d", l.version)
	}

	var owner [16]byte // MAX_NAME_LENGTH
	if err := binary.Read(l.file, binary.BigEndian, &owner); err != nil {
		return fmt.Errorf("ghost: read owner: %w", err)
	}

	var mapName [64]byte
	if err := binary.Read(l.file, binary.BigEndian, &mapName); err != nil {
		return fmt.Errorf("ghost: read map: %w", err)
	}

	var zeroes [4]byte // CRC before version 6
	if err := binary.Read(l.file, binary.BigEndian, &zeroes); err != nil {
		return fmt.Errorf("ghost: read zeroes: %w", err)
	}

	var numTicksBuf [4]byte
	if err := binary.Read(l.file, binary.BigEndian, &numTicksBuf); err != nil {
		return fmt.Errorf("ghost: read numticks: %w", err)
	}

	var timeBuf [4]byte
	if err := binary.Read(l.file, binary.BigEndian, &timeBuf); err != nil {
		return fmt.Errorf("ghost: read time: %w", err)
	}

	if l.version >= 6 {
		var sha256 [32]byte
		if err := binary.Read(l.file, binary.BigEndian, &sha256); err != nil {
			return fmt.Errorf("ghost: read sha256: %w", err)
		}
	}

	numTicks := int(binary.BigEndian.Uint32(numTicksBuf[:]))
	timeCentis := int(binary.BigEndian.Uint32(timeBuf[:]))

	l.info = replay.RecordingInfo{
		Format:      replay.FormatGhost,
		Map:         packet.CString(mapName[:]),
		Player:      packet.CString(owner[:]),
		NumTicks:    numTicks,
		TimeCentis:  timeCentis,
		SelectedCID: -1,
	}

	return nil
}

// NextCharacter returns the next character frame from the ghost file.
func (l *Loader) NextCharacter() (replay.CharacterFrame, error) {
	if l.done {
		return replay.CharacterFrame{}, io.EOF
	}

	for {
		typ, err := l.readNextType()
		if err != nil {
			l.done = true
			return replay.CharacterFrame{}, io.EOF
		}

		switch typ {
		case ghostDataTypeStartTick:
			var buf [startTickSize]byte
			if !l.readData(typ, buf[:]) {
				l.done = true
				return replay.CharacterFrame{}, io.EOF
			}
			l.startTick = int(int32(binary.BigEndian.Uint32(buf[:])))

		case ghostDataTypeSkin:
			var buf [skinSize]byte
			if !l.readData(typ, buf[:]) {
				l.done = true
				return replay.CharacterFrame{}, io.EOF
			}
			// Skip skin data

		case ghostDataTypeCharacterNoTick:
			var buf [characterNoTickSize]byte
			if !l.readData(typ, buf[:]) {
				l.done = true
				return replay.CharacterFrame{}, io.EOF
			}
			frame := parseCharacterNoTick(buf[:])
			if l.startTick >= 0 {
				frame.Tick = l.startTick + l.frameIdx
			} else {
				frame.Tick = l.frameIdx
			}
			l.frameIdx++
			return frame, nil

		case ghostDataTypeCharacter:
			var buf [characterSize]byte
			if !l.readData(typ, buf[:]) {
				l.done = true
				return replay.CharacterFrame{}, io.EOF
			}
			frame := parseCharacter(buf[:])
			l.frameIdx++
			return frame, nil

		default:
			l.done = true
			return replay.CharacterFrame{}, io.EOF
		}
	}
}

// Info returns metadata about the ghost recording.
func (l *Loader) Info() replay.RecordingInfo {
	return l.info
}

// Close releases the ghost file.
func (l *Loader) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// readNextType reads the next item type from the current chunk or reads a new chunk.
func (l *Loader) readNextType() (int, error) {
	if l.currentItem != l.prevItem && l.currentItem < l.numItems {
		l.prevItem = l.currentItem
		return l.lastType, nil
	}
	return l.readChunk()
}

// readChunk reads the next chunk from the file.
func (l *Loader) readChunk() (int, error) {
	if l.version != 4 {
		l.lastType = -1
		for i := range l.lastData {
			l.lastData[i] = 0
		}
	}

	l.currentItem = 0
	l.bufferPos = 0
	l.numItems = 0

	var chunkHeader [4]byte
	if _, err := io.ReadFull(l.file, chunkHeader[:]); err != nil {
		return 0, err
	}

	typ := int(chunkHeader[0])
	numItems := int(chunkHeader[1])
	size := (int(chunkHeader[2]) << 8) | int(chunkHeader[3])

	if size <= 0 || size > maxChunkSize {
		return 0, fmt.Errorf("ghost: invalid chunk size %d", size)
	}

	compressed := make([]byte, size)
	if _, err := io.ReadFull(l.file, compressed); err != nil {
		return 0, fmt.Errorf("ghost: read chunk data: %w", err)
	}

	// Huffman decompress
	decompressed, err := huffman.Decompress(compressed)
	if err != nil {
		return 0, fmt.Errorf("ghost: huffman decompress: %w", err)
	}

	// Variable-int decompress
	l.buffer = make([]byte, maxChunkSize)
	finalSize := packet.VarintDecompress(decompressed, l.buffer)
	if finalSize < 0 {
		return 0, fmt.Errorf("ghost: varint decompress failed")
	}

	l.bufferEnd = finalSize
	l.numItems = numItems
	l.lastType = typ
	l.currentItem = 0
	l.prevItem = -1
	l.bufferPos = 0

	return typ, nil
}

// readData reads item data, applying undiff if same type as last.
func (l *Loader) readData(typ int, out []byte) bool {
	size := len(out)
	if size%4 != 0 || size > maxItemSize {
		return false
	}
	if l.bufferPos+size > l.bufferEnd {
		return false
	}

	if l.lastType == typ {
		for i := 0; i < size; i += 4 {
			past := binary.LittleEndian.Uint32(l.lastData[i : i+4])
			diff := binary.LittleEndian.Uint32(l.buffer[l.bufferPos+i : l.bufferPos+i+4])
			binary.LittleEndian.PutUint32(out[i:i+4], past+diff)
		}
	} else {
		copy(out, l.buffer[l.bufferPos:l.bufferPos+size])
	}

	copy(l.lastData[:size], out)
	l.lastType = typ
	l.bufferPos += size
	l.currentItem++
	return true
}

func parseCharacterNoTick(data []byte) replay.CharacterFrame {
	return replay.CharacterFrame{
		X:          packet.ReadInt32LE(data[0:4]),
		Y:          packet.ReadInt32LE(data[4:8]),
		VelX:       packet.ReadInt32LE(data[8:12]),
		VelY:       packet.ReadInt32LE(data[12:16]),
		Angle:      packet.ReadInt32LE(data[16:20]),
		Direction:  packet.ReadInt32LE(data[20:24]),
		Weapon:     replay.CharWeapon(packet.ReadInt32LE(data[24:28])),
		HookState:  replay.CharHookState(packet.ReadInt32LE(data[28:32])),
		HookX:      packet.ReadInt32LE(data[32:36]),
		HookY:      packet.ReadInt32LE(data[36:40]),
		AttackTick: packet.ReadInt32LE(data[40:44]),
	}
}

func parseCharacter(data []byte) replay.CharacterFrame {
	f := parseCharacterNoTick(data)
	f.Tick = packet.ReadInt32LE(data[44:48])
	return f
}
