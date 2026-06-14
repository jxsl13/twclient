package demo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/jxsl13/twclient/packer"
	"github.com/teeworlds-go/huffman/v2"
)

// Magic is the 7-byte file marker gs_aHeaderMarker {'T','W','D','E','M','O',0}
// (DDNet src/engine/demo.h).
var Magic = [7]byte{'T', 'W', 'D', 'E', 'M', 'O', 0}

// Version is the current demo format version written by DDNet (gs_CurVersion).
const Version uint8 = 6

// Demo format version thresholds (DDNet demo.cpp).
const (
	versionOld             = 3 // gs_OldVersion: timeline markers exist above this
	versionTickCompression = 5 // gs_VersionTickCompression: CHUNKTICKFLAG_TICK_COMPRESSED
	versionSha256          = 6 // gs_Sha256Version: SHA256 extension exists at/above this
)

// Fixed on-disk sizes.
const (
	headerSize         = 176 // sizeof(CDemoHeader)
	maxTimelineMarkers = 64  // MAX_TIMELINE_MARKERS
	timelineSize       = 4 + maxTimelineMarkers*4
	sha256DigestSize   = 32
)

// sha256ExtensionUUID is SHA256_EXTENSION ("demoitem-sha256@ddnet.tw"), written
// before the embedded map for version >= 6 demos (DDNet demo.cpp).
var sha256ExtensionUUID = [16]byte{
	0x6b, 0xe6, 0xda, 0x4a, 0xce, 0xbd, 0x38, 0x0c,
	0x9b, 0x5b, 0x12, 0x89, 0xc8, 0x42, 0xd7, 0x80,
}

// Chunk header flags and masks (DDNet demo.cpp).
const (
	chunkTypeFlagTickmarker     = 0x80
	chunkTickFlagKeyframe       = 0x40
	chunkTickFlagTickCompressed = 0x20
	chunkMaskTick               = 0x1f
	chunkMaskTickLegacy         = 0x3f
	chunkMaskType               = 0x60
	chunkMaskSize               = 0x1f
)

// ChunkType is the kind of a non-tickmarker [DataChunk] (CHUNKTYPE_* in DDNet).
type ChunkType int

// Data chunk types stored in bits 5-6 of the chunk's leading byte.
const (
	ChunkTypeSnapshot ChunkType = 1
	ChunkTypeMessage  ChunkType = 2
	ChunkTypeDelta    ChunkType = 3
)

func (t ChunkType) String() string {
	switch t {
	case ChunkTypeSnapshot:
		return "snapshot"
	case ChunkTypeMessage:
		return "message"
	case ChunkTypeDelta:
		return "delta"
	default:
		return fmt.Sprintf("ChunkType(%d)", int(t))
	}
}

// Header is everything that precedes the chunk stream: the fixed CDemoHeader, the
// optional timeline markers, the optional SHA256 extension and the embedded map.
// The typed fields are decoded from / re-encoded to their exact on-disk bytes, so
// a parsed Header re-serializes byte-for-byte.
type Header struct {
	Version    uint8  // demo format version (gs_CurVersion is 6)
	NetVersion string // network protocol string, e.g. "0.6 626fce9a778df4d4"
	MapName    string // map name without extension
	MapSize    uint32 // embedded map size in bytes (big-endian on disk)
	MapCrc     uint32 // map CRC32 (big-endian on disk)
	Type       string // recording type, e.g. "client" / "server"
	Length     uint32 // demo length in ticks, filled in on stop (big-endian on disk)
	Timestamp  string // local recording timestamp, e.g. "2026-04-08_21-52-08"

	// TimelineMarkers holds the demo's timeline markers (present for Version > 3).
	// At most MAX_TIMELINE_MARKERS (64) are stored on disk.
	TimelineMarkers []int32
	// Sha256 is the embedded map digest from the SHA256 extension (present for
	// Version >= 6 when the extension UUID matches), or nil.
	Sha256 *[32]byte
	// MapData is the raw embedded map (MapSize bytes).
	MapData []byte
}

// File is a parsed demo file: its Header preamble plus the chunk stream in order.
type File struct {
	Header Header
	Chunks []Chunk
}

// Chunk is one item of the demo chunk stream: a [TickMarker] or a [DataChunk].
type Chunk interface{ isChunk() }

type (
	// TickMarker starts a new tick. On the wire it is either an absolute 4-byte
	// tick or a compressed single-byte delta vs the previous marker; the form is
	// derived from Tick/Keyframe on write, exactly as DDNet's recorder does.
	TickMarker struct {
		Tick     int
		Keyframe bool
	}
	// DataChunk is a snapshot, snapshot delta or network message. Payload holds
	// the original huffman-compressed teeworlds-varint bytes verbatim (so the
	// chunk round-trips byte-for-byte); use [DataChunk.Decompress] / [DataChunk.Ints]
	// to decode it.
	DataChunk struct {
		Type    ChunkType
		Payload []byte
	}
)

func (TickMarker) isChunk() {}
func (DataChunk) isChunk()  {}

// Decompress returns the huffman-decompressed payload (still teeworlds-varint
// packed). For snapshot/delta chunks this is the intpacked snapshot data; for
// message chunks the packed message.
func (c DataChunk) Decompress() ([]byte, error) {
	return huffman.Decompress(c.Payload)
}

// Ints decompresses the payload and unpacks it into the teeworlds-varint integer
// stream it encodes (the trailing zero ints come from DDNet's 4-byte alignment
// padding of the data before compression).
func (c DataChunk) Ints() ([]int, error) {
	raw, err := c.Decompress()
	if err != nil {
		return nil, err
	}
	u := packer.NewUnpacker(raw)
	var out []int
	for u.RemainingSize() > 0 {
		v, err := u.NextInt()
		if err != nil {
			return nil, fmt.Errorf("demo: unpack int: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}

// cstr reads a NUL-terminated (or full) string out of a fixed-size field.
func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// parseFixedHeader decodes the 176-byte CDemoHeader.
func parseFixedHeader(b []byte) (Header, error) {
	if len(b) < headerSize {
		return Header{}, fmt.Errorf("demo: header needs %d bytes, have %d", headerSize, len(b))
	}
	if !bytes.Equal(b[0:7], Magic[:]) {
		return Header{}, errors.New("demo: bad magic")
	}
	var h Header
	h.Version = b[7]
	h.NetVersion = cstr(b[8:72])
	h.MapName = cstr(b[72:136])
	h.MapSize = binary.BigEndian.Uint32(b[136:140])
	h.MapCrc = binary.BigEndian.Uint32(b[140:144])
	h.Type = cstr(b[144:152])
	h.Length = binary.BigEndian.Uint32(b[152:156])
	h.Timestamp = cstr(b[156:176])
	return h, nil
}

// marshalFixedHeader re-encodes the 176-byte CDemoHeader, mirroring DDNet's
// mem_zero + str_copy + uint_to_bytes_be layout exactly.
func marshalFixedHeader(h Header) []byte {
	b := make([]byte, headerSize)
	copy(b[0:7], Magic[:])
	b[7] = h.Version
	copy(b[8:72], h.NetVersion) // zero-padded, NUL-terminated by the zero fill
	copy(b[72:136], h.MapName)
	binary.BigEndian.PutUint32(b[136:140], h.MapSize)
	binary.BigEndian.PutUint32(b[140:144], h.MapCrc)
	copy(b[144:152], h.Type)
	binary.BigEndian.PutUint32(b[152:156], h.Length)
	copy(b[156:176], h.Timestamp)
	return b
}

// parseTimeline decodes the 260-byte CTimelineMarkers block.
func parseTimeline(b []byte) []int32 {
	num := int(binary.BigEndian.Uint32(b[0:4]))
	if num > maxTimelineMarkers {
		num = maxTimelineMarkers
	}
	markers := make([]int32, num)
	for i := 0; i < num; i++ {
		markers[i] = int32(binary.BigEndian.Uint32(b[4+i*4 : 8+i*4]))
	}
	return markers
}

// appendTimeline re-encodes the 260-byte CTimelineMarkers block.
func appendTimeline(b []byte, markers []int32) []byte {
	num := len(markers)
	if num > maxTimelineMarkers {
		num = maxTimelineMarkers
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(num))
	b = append(b, n[:]...)
	for i := 0; i < maxTimelineMarkers; i++ {
		var m [4]byte
		if i < num {
			binary.BigEndian.PutUint32(m[:], uint32(markers[i]))
		}
		b = append(b, m[:]...)
	}
	return b
}

// marshalPreamble re-encodes everything before the chunk stream: fixed header,
// timeline markers, SHA256 extension and embedded map data.
func (h Header) marshalPreamble() []byte {
	b := make([]byte, 0, headerSize+timelineSize+16+sha256DigestSize+len(h.MapData))
	b = append(b, marshalFixedHeader(h)...)
	if int(h.Version) > versionOld {
		b = appendTimeline(b, h.TimelineMarkers)
	}
	if int(h.Version) >= versionSha256 && h.Sha256 != nil {
		b = append(b, sha256ExtensionUUID[:]...)
		b = append(b, h.Sha256[:]...)
	}
	b = append(b, h.MapData...)
	return b
}

// Parse reads a whole demo file into memory.
func Parse(r io.Reader) (*File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("demo: read: %w", err)
	}
	if len(data) < headerSize {
		return nil, errors.New("demo: too short for header")
	}

	h, err := parseFixedHeader(data[:headerSize])
	if err != nil {
		return nil, err
	}
	pos := headerSize

	if int(h.Version) > versionOld {
		if len(data) < pos+timelineSize {
			return nil, errors.New("demo: truncated timeline markers")
		}
		h.TimelineMarkers = parseTimeline(data[pos : pos+timelineSize])
		pos += timelineSize
	}

	if int(h.Version) >= versionSha256 {
		if len(data) >= pos+16 && bytes.Equal(data[pos:pos+16], sha256ExtensionUUID[:]) {
			pos += 16
			if len(data) < pos+sha256DigestSize {
				return nil, errors.New("demo: truncated SHA256 digest")
			}
			var d [32]byte
			copy(d[:], data[pos:pos+sha256DigestSize])
			h.Sha256 = &d
			pos += sha256DigestSize
		}
		// Otherwise the bytes are the start of the map data (DDNet rewinds).
	}

	end := pos + int(h.MapSize)
	if int(h.MapSize) < 0 || end < pos || end > len(data) {
		return nil, fmt.Errorf("demo: map size %d exceeds file", h.MapSize)
	}
	h.MapData = data[pos:end]
	pos = end

	f := &File{Header: h}
	c := &sliceReader{data: data, pos: pos}
	prevTick := -1
	for c.remaining() > 0 {
		ch, err := readChunk(c, h.Version, &prevTick)
		if err != nil {
			return nil, err
		}
		f.Chunks = append(f.Chunks, ch)
	}
	return f, nil
}

// sliceReader is a minimal forward cursor over an in-memory buffer.
type sliceReader struct {
	data []byte
	pos  int
}

func (c *sliceReader) remaining() int { return len(c.data) - c.pos }

func (c *sliceReader) byte() (byte, error) {
	if c.pos >= len(c.data) {
		return 0, io.ErrUnexpectedEOF
	}
	b := c.data[c.pos]
	c.pos++
	return b, nil
}

func (c *sliceReader) next(n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("demo: negative length %d", n)
	}
	if c.pos+n > len(c.data) {
		return nil, io.ErrUnexpectedEOF
	}
	b := c.data[c.pos : c.pos+n]
	c.pos += n
	return b, nil
}

// readChunk decodes one chunk from the slice cursor, mirroring DDNet's
// ReadChunkHeader + payload read. prevTick carries the running tick value used to
// resolve compressed tick deltas.
func readChunk(c *sliceReader, version uint8, prevTick *int) (Chunk, error) {
	b, err := c.byte()
	if err != nil {
		return nil, err
	}
	if b&chunkTypeFlagTickmarker != 0 {
		keyframe := b&chunkTickFlagKeyframe != 0
		if delta, absolute := tickDelta(b, version); absolute {
			raw, err := c.next(4)
			if err != nil {
				return nil, fmt.Errorf("demo: tick marker: %w", err)
			}
			tick := int(int32(binary.BigEndian.Uint32(raw)))
			*prevTick = tick
			return TickMarker{Tick: tick, Keyframe: keyframe}, nil
		} else {
			if *prevTick < 0 {
				return nil, errors.New("demo: tick delta before initial tick")
			}
			tick := *prevTick + delta
			*prevTick = tick
			return TickMarker{Tick: tick, Keyframe: keyframe}, nil
		}
	}

	typ := ChunkType((int(b) & chunkMaskType) >> 5)
	size := int(b) & chunkMaskSize
	switch size {
	case 30:
		s, err := c.next(1)
		if err != nil {
			return nil, fmt.Errorf("demo: chunk size: %w", err)
		}
		size = int(s[0])
	case 31:
		s, err := c.next(2)
		if err != nil {
			return nil, fmt.Errorf("demo: chunk size: %w", err)
		}
		size = int(s[1])<<8 | int(s[0])
	}
	payload, err := c.next(size)
	if err != nil {
		return nil, fmt.Errorf("demo: chunk payload: %w", err)
	}
	return DataChunk{Type: typ, Payload: payload}, nil
}

// tickDelta classifies a tickmarker leading byte: when absolute is true the tick
// is a following 4-byte big-endian value, otherwise delta is the increment over
// the previous tick.
func tickDelta(b byte, version uint8) (delta int, absolute bool) {
	if version < versionTickCompression && int(b)&chunkMaskTickLegacy != 0 {
		return int(b) & chunkMaskTickLegacy, false
	}
	if b&chunkTickFlagTickCompressed != 0 {
		return int(b) & chunkMaskTick, false
	}
	return 0, true
}

// WriteTo re-serializes the file, byte-identical to a DDNet-written input: the
// preamble (header + timeline + SHA256 ext + map) followed by the chunk stream.
func (f *File) WriteTo(w io.Writer) (int64, error) {
	b := f.Header.marshalPreamble()
	prevTick := -1
	for _, ch := range f.Chunks {
		b = appendChunk(b, ch, f.Header.Version, &prevTick)
	}
	n, err := w.Write(b)
	return int64(n), err
}

// appendChunk encodes one chunk, mirroring DDNet's WriteTickMarker / Write.
func appendChunk(b []byte, ch Chunk, version uint8, prevTick *int) []byte {
	switch c := ch.(type) {
	case TickMarker:
		return appendTickMarker(b, c, prevTick)
	case DataChunk:
		return appendDataChunk(b, c)
	default:
		return b
	}
}

// appendTickMarker reproduces CDemoRecorder::WriteTickMarker (version >= 5 form):
// an absolute 4-byte tick for the first marker, keyframes, or deltas above 31;
// otherwise a single compressed delta byte.
func appendTickMarker(b []byte, m TickMarker, prevTick *int) []byte {
	last := *prevTick
	if last == -1 || m.Tick-last > chunkMaskTick || m.Keyframe {
		head := byte(chunkTypeFlagTickmarker)
		if m.Keyframe {
			head |= chunkTickFlagKeyframe
		}
		var t [4]byte
		binary.BigEndian.PutUint32(t[:], uint32(m.Tick))
		b = append(b, head)
		b = append(b, t[:]...)
	} else {
		head := byte(chunkTypeFlagTickmarker | chunkTickFlagTickCompressed | (m.Tick - last))
		b = append(b, head)
	}
	*prevTick = m.Tick
	return b
}

// appendDataChunk reproduces CDemoRecorder::Write's chunk header + payload.
func appendDataChunk(b []byte, c DataChunk) []byte {
	size := len(c.Payload)
	head := byte((int(c.Type) & 0x3) << 5)
	switch {
	case size < 30:
		b = append(b, head|byte(size))
	case size < 256:
		b = append(b, head|30, byte(size&0xff))
	default:
		b = append(b, head|31, byte(size&0xff), byte(size>>8))
	}
	return append(b, c.Payload...)
}
