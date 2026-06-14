package ghost

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/jxsl13/twclient/packer"
	"github.com/teeworlds-go/huffman/v2"
)

// Magic is the 8-byte file marker gs_aHeaderMarker = {'T','W','G','H','O','S','T',0}
// (DDNet src/engine/client/ghost.cpp).
var Magic = [8]byte{'T', 'W', 'G', 'H', 'O', 'S', 'T', 0}

// Version is the current ghost format version (gs_CurVersion). Versions 4..6 are
// accepted on read; the map SHA256 header field exists only from version 6.
const Version uint8 = 6

// MinVersion is the oldest supported ghost version.
const MinVersion uint8 = 4

// Layout constants from DDNet ghost.h.
const (
	MaxItemSize      = 128                            // MAX_ITEM_SIZE
	NumItemsPerChunk = 50                             // NUM_ITEMS_PER_CHUNK
	MaxChunkSize     = MaxItemSize * NumItemsPerChunk // MAX_CHUNK_SIZE
	MaxNameLength    = 16                             // MAX_NAME_LENGTH
	MapNameLength    = 64                             // CGhostHeader::m_aMap
	Sha256Size       = 32                             // SHA256_DIGEST
)

// DataType identifies the kind of item stored in a chunk (GHOSTDATA_TYPE_*,
// DDNet src/game/client/components/ghost.h).
const (
	TypeSkin            = 0 // GHOSTDATA_TYPE_SKIN
	TypeCharacterNoTick = 1 // GHOSTDATA_TYPE_CHARACTER_NO_TICK
	TypeCharacter       = 2 // GHOSTDATA_TYPE_CHARACTER
	TypeStartTick       = 3 // GHOSTDATA_TYPE_START_TICK
)

// Header is the decoded CGhostHeader. Owner and Map are the NUL-terminated
// strings from their fixed-size fields; the remaining fields are stored exactly
// as on the wire so WriteTo reproduces a DDNet-written header byte-for-byte.
type Header struct {
	Version   uint8            // m_Version (4..6)
	Owner     string           // m_aOwner[16], NUL-terminated
	Map       string           // m_aMap[64], NUL-terminated
	MapCRC    [4]byte          // m_aZeroes: map CRC before v6, zero from v6 (big-endian)
	NumTicks  int              // m_aNumTicks (big-endian)
	Time      int              // m_aTime (big-endian)
	MapSha256 [Sha256Size]byte // m_MapSha256, present only when Version >= 6
}

// File is a parsed ghost file: its header and every item in stream order.
type File struct {
	Header Header
	Chunks []Chunk
}

// Chunk is one ghost item with its fields resolved to absolute values. It is a
// sum type implemented by Skin, CharacterNoTick, Character, StartTick and Raw.
type Chunk interface {
	isChunk()
	dataType() int   // GHOSTDATA_TYPE_* of this item
	fields() []int32 // absolute field values, in struct order
}

type (
	// Skin is a CGhostSkin (GHOSTDATA_TYPE_SKIN).
	Skin struct {
		Skin           [6]int
		UseCustomColor int
		ColorBody      int
		ColorFeet      int
	}
	// CharacterNoTick is a CGhostCharacter_NoTick (GHOSTDATA_TYPE_CHARACTER_NO_TICK).
	CharacterNoTick struct {
		X, Y       int
		VelX, VelY int
		Angle      int
		Direction  int
		Weapon     int
		HookState  int
		HookX      int
		HookY      int
		AttackTick int
	}
	// Character is a CGhostCharacter (GHOSTDATA_TYPE_CHARACTER): a NoTick plus a tick.
	Character struct {
		CharacterNoTick
		Tick int
	}
	// StartTick is the recording's start tick (GHOSTDATA_TYPE_START_TICK).
	StartTick struct{ Tick int }
	// Raw preserves an item of an unrecognized type (or unexpected field count)
	// verbatim so it still round-trips byte-for-byte.
	Raw struct {
		Type   int
		Values []int32
	}
)

func (Skin) isChunk()            {}
func (CharacterNoTick) isChunk() {}
func (Character) isChunk()       {}
func (StartTick) isChunk()       {}
func (Raw) isChunk()             {}

func (Skin) dataType() int            { return TypeSkin }
func (CharacterNoTick) dataType() int { return TypeCharacterNoTick }
func (Character) dataType() int       { return TypeCharacter }
func (StartTick) dataType() int       { return TypeStartTick }
func (c Raw) dataType() int           { return c.Type }

func (c Skin) fields() []int32 {
	return []int32{
		int32(c.Skin[0]), int32(c.Skin[1]), int32(c.Skin[2]),
		int32(c.Skin[3]), int32(c.Skin[4]), int32(c.Skin[5]),
		int32(c.UseCustomColor), int32(c.ColorBody), int32(c.ColorFeet),
	}
}

func (c CharacterNoTick) fields() []int32 {
	return []int32{
		int32(c.X), int32(c.Y), int32(c.VelX), int32(c.VelY),
		int32(c.Angle), int32(c.Direction), int32(c.Weapon), int32(c.HookState),
		int32(c.HookX), int32(c.HookY), int32(c.AttackTick),
	}
}

func (c Character) fields() []int32 {
	return append(c.CharacterNoTick.fields(), int32(c.Tick))
}

func (c StartTick) fields() []int32 { return []int32{int32(c.Tick)} }

func (c Raw) fields() []int32 { return c.Values }

// chunkFromFields reconstructs a typed Chunk from one item's absolute fields,
// falling back to Raw for unknown types or unexpected field counts.
func chunkFromFields(typ int, f []int32) Chunk {
	switch typ {
	case TypeSkin:
		if len(f) == 9 {
			return Skin{
				Skin:           [6]int{int(f[0]), int(f[1]), int(f[2]), int(f[3]), int(f[4]), int(f[5])},
				UseCustomColor: int(f[6]), ColorBody: int(f[7]), ColorFeet: int(f[8]),
			}
		}
	case TypeCharacterNoTick:
		if len(f) == 11 {
			return characterNoTickFromFields(f)
		}
	case TypeCharacter:
		if len(f) == 12 {
			return Character{CharacterNoTick: characterNoTickFromFields(f[:11]), Tick: int(f[11])}
		}
	case TypeStartTick:
		if len(f) == 1 {
			return StartTick{Tick: int(f[0])}
		}
	}
	return Raw{Type: typ, Values: append([]int32(nil), f...)}
}

func characterNoTickFromFields(f []int32) CharacterNoTick {
	return CharacterNoTick{
		X: int(f[0]), Y: int(f[1]), VelX: int(f[2]), VelY: int(f[3]),
		Angle: int(f[4]), Direction: int(f[5]), Weapon: int(f[6]), HookState: int(f[7]),
		HookX: int(f[8]), HookY: int(f[9]), AttackTick: int(f[10]),
	}
}

// Parse reads a whole ghost file.
func Parse(r io.Reader) (*File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("ghost: read: %w", err)
	}
	hdr, n, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	f := &File{Header: hdr}
	body := data[n:]
	for len(body) > 0 {
		typ, numItems, payload, rest, err := splitChunk(body)
		if err != nil {
			return nil, err
		}
		chunks, err := decodePhysicalChunk(typ, numItems, payload)
		if err != nil {
			return nil, err
		}
		f.Chunks = append(f.Chunks, chunks...)
		body = rest
	}
	return f, nil
}

// parseHeader decodes a CGhostHeader from the front of data and returns the
// number of header bytes consumed.
func parseHeader(data []byte) (Header, int, error) {
	// Header size without the SHA256 (read first, like CGhostLoader::ReadHeader).
	const base = len(Magic) + 1 + MaxNameLength + MapNameLength + 4 + 4 + 4
	if len(data) < base {
		return Header{}, 0, fmt.Errorf("ghost: too short for header: %w", io.ErrUnexpectedEOF)
	}
	if !bytes.Equal(data[:len(Magic)], Magic[:]) {
		return Header{}, 0, errors.New("ghost: bad magic")
	}
	p := len(Magic)
	var h Header
	h.Version = data[p]
	p++
	if h.Version < MinVersion || h.Version > Version {
		return Header{}, 0, fmt.Errorf("ghost: unsupported version %d", h.Version)
	}

	owner, err := nulString(data[p : p+MaxNameLength])
	if err != nil {
		return Header{}, 0, fmt.Errorf("ghost: owner: %w", err)
	}
	h.Owner = owner
	p += MaxNameLength

	mapName, err := nulString(data[p : p+MapNameLength])
	if err != nil {
		return Header{}, 0, fmt.Errorf("ghost: map: %w", err)
	}
	h.Map = mapName
	p += MapNameLength

	copy(h.MapCRC[:], data[p:p+4])
	p += 4
	h.NumTicks = int(binary.BigEndian.Uint32(data[p : p+4]))
	p += 4
	h.Time = int(binary.BigEndian.Uint32(data[p : p+4]))
	p += 4

	if h.Version >= 6 {
		if len(data) < p+Sha256Size {
			return Header{}, 0, fmt.Errorf("ghost: too short for map sha256: %w", io.ErrUnexpectedEOF)
		}
		copy(h.MapSha256[:], data[p:p+Sha256Size])
		p += Sha256Size
	}
	return h, p, nil
}

// nulString returns the NUL-terminated string at the start of b; the terminator
// must be present (DDNet ValidateHeader requires mem_has_null).
func nulString(b []byte) (string, error) {
	before, _, ok := bytes.Cut(b, []byte{0})
	if !ok {
		return "", errors.New("missing NUL terminator")
	}
	return string(before), nil
}

// splitChunk reads one physical chunk header and payload off the front of body.
func splitChunk(body []byte) (typ, numItems int, payload, rest []byte, err error) {
	if len(body) < 4 {
		return 0, 0, nil, nil, fmt.Errorf("ghost: chunk header truncated: %w", io.ErrUnexpectedEOF)
	}
	typ = int(body[0])
	numItems = int(body[1])
	size := int(body[2])<<8 | int(body[3])
	if size <= 0 || size > MaxChunkSize {
		return 0, 0, nil, nil, fmt.Errorf("ghost: invalid chunk size %d", size)
	}
	if len(body) < 4+size {
		return 0, 0, nil, nil, fmt.Errorf("ghost: chunk data truncated: %w", io.ErrUnexpectedEOF)
	}
	return typ, numItems, body[4 : 4+size], body[4+size:], nil
}

// decodePhysicalChunk huffman-decompresses payload, intpack-decodes it to int32
// fields, splits them into numItems items, un-diffs them to absolute values, and
// returns one Chunk per item.
func decodePhysicalChunk(typ, numItems int, payload []byte) ([]Chunk, error) {
	if numItems <= 0 {
		return nil, fmt.Errorf("ghost: invalid item count %d", numItems)
	}
	huff, err := huffman.Decompress(payload)
	if err != nil {
		return nil, fmt.Errorf("ghost: huffman decompress: %w", err)
	}
	ints, err := unpackInts(huff)
	if err != nil {
		return nil, fmt.Errorf("ghost: intpack decompress: %w", err)
	}
	if len(ints) == 0 || len(ints)%numItems != 0 {
		return nil, fmt.Errorf("ghost: %d fields not divisible by %d items", len(ints), numItems)
	}
	fpi := len(ints) / numItems
	if fpi*4 > MaxItemSize {
		return nil, fmt.Errorf("ghost: item too large: %d bytes", fpi*4)
	}

	chunks := make([]Chunk, 0, numItems)
	prev := make([]int32, fpi)
	for i := range numItems {
		raw := ints[i*fpi : (i+1)*fpi]
		cur := make([]int32, fpi)
		if i == 0 {
			copy(cur, raw)
		} else {
			for j := range fpi {
				cur[j] = int32(uint32(prev[j]) + uint32(raw[j]))
			}
		}
		chunks = append(chunks, chunkFromFields(typ, cur))
		prev = cur
	}
	return chunks, nil
}

// unpackInts reads every teeworlds varint in data as an int32.
func unpackInts(data []byte) ([]int32, error) {
	u := packer.NewUnpacker(data)
	out := make([]int32, 0, len(data))
	for u.RemainingSize() > 0 {
		v, err := u.NextInt()
		if err != nil {
			return nil, err
		}
		out = append(out, int32(v))
	}
	return out, nil
}

// WriteTo re-serializes the file, byte-identical to a DDNet-written input: the
// fixed header followed by physical chunks regrouped exactly as CGhostRecorder
// does (runs of one type, flushed every NumItemsPerChunk).
func (f *File) WriteTo(w io.Writer) (int64, error) {
	b := appendHeader(nil, f.Header)
	i := 0
	for i < len(f.Chunks) {
		typ := f.Chunks[i].dataType()
		var items [][]int32
		j := i
		for j < len(f.Chunks) && len(items) < NumItemsPerChunk && f.Chunks[j].dataType() == typ {
			items = append(items, f.Chunks[j].fields())
			j++
		}
		var err error
		b, err = appendPhysicalChunk(b, typ, items)
		if err != nil {
			return 0, err
		}
		i = j
	}
	n, err := w.Write(b)
	return int64(n), err
}

// appendHeader writes a CGhostHeader, zero-padding the fixed-size name fields
// exactly as DDNet's str_copy does.
func appendHeader(b []byte, h Header) []byte {
	b = append(b, Magic[:]...)
	b = append(b, h.Version)
	var owner [MaxNameLength]byte
	copy(owner[:], h.Owner)
	b = append(b, owner[:]...)
	var mapName [MapNameLength]byte
	copy(mapName[:], h.Map)
	b = append(b, mapName[:]...)
	b = append(b, h.MapCRC[:]...)
	b = binary.BigEndian.AppendUint32(b, uint32(h.NumTicks))
	b = binary.BigEndian.AppendUint32(b, uint32(h.Time))
	if h.Version >= 6 {
		b = append(b, h.MapSha256[:]...)
	}
	return b
}

// appendPhysicalChunk re-diffs items against their predecessors, intpack-packs
// them, huffman-compresses the result and appends the 4-byte chunk header and
// payload — the inverse of decodePhysicalChunk.
func appendPhysicalChunk(b []byte, typ int, items [][]int32) ([]byte, error) {
	if len(items) == 0 {
		return b, nil
	}
	var ints []int32
	ints = append(ints, items[0]...)
	prev := items[0]
	for k := 1; k < len(items); k++ {
		cur := items[k]
		for j := range cur {
			ints = append(ints, int32(uint32(cur[j])-uint32(prev[j])))
		}
		prev = cur
	}

	var packed []byte
	for _, v := range ints {
		packed = packer.AppendInt(packed, int(v))
	}
	comp, err := huffman.Compress(packed)
	if err != nil {
		return nil, fmt.Errorf("ghost: huffman compress: %w", err)
	}
	if len(comp) > MaxChunkSize {
		return nil, fmt.Errorf("ghost: compressed chunk too large: %d bytes", len(comp))
	}
	b = append(b, byte(typ), byte(len(items)), byte(len(comp)>>8), byte(len(comp)&0xff))
	b = append(b, comp...)
	return b, nil
}
