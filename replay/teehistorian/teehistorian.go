// Package teehistorian parses DDNet teehistorian files which record all
// server-side inputs for every connected player.
//
// This is the most valuable format for ML training because it contains
// actual raw player inputs (INPUT_NEW / INPUT_DIFF with all 10
// CNetObj_PlayerInput fields) at server tick granularity.
//
// Format specification: https://ddnet.org/libtw2-doc/teehistorian/
package teehistorian

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/replay"
	"github.com/teeworlds-go/varint"
)

const (
	MsgFinish         = -1
	MsgTickSkip       = -2
	MsgPlayerNew      = -3
	MsgPlayerOld      = -4
	MsgInputDiff      = -5
	MsgInputNew       = -6
	MsgMessage        = -7
	MsgJoin           = -8
	MsgDrop           = -9
	MsgConsoleCommand = -10
	MsgEx             = -11

	MaxDirectPlayerDiff = 63
	MaxClients          = 64
)

// HeaderUUID is the 16-byte UUID that identifies teehistorian files.
var HeaderUUID = [16]byte{
	0x69, 0x9d, 0xb1, 0x7b, 0x8e, 0xfb, 0x34, 0xff,
	0xb1, 0xd8, 0xda, 0x6f, 0x60, 0xc1, 0x5d, 0xd1,
}

// HeaderJSON is the JSON structure embedded in the teehistorian file header.
type HeaderJSON struct {
	Version    string `json:"version"`
	GameType   string `json:"game_type"`
	Map        string `json:"map"`
	ServerName string `json:"server_name"`
}

// Loader reads teehistorian files and provides input frames.
type Loader struct {
	data []byte
	pos  int
	info replay.RecordingInfo

	cid  int // selected client ID (-1 = first player seen)
	tick int

	inputs   [MaxClients][packet.InputFields]int
	hasInput [MaxClients]bool
	alive    [MaxClients]bool

	done bool
}

// Open opens a teehistorian file and reads the header.
func Open(filename string, cid int) (*Loader, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("teehistorian: open %s: %w", filename, err)
	}

	l := &Loader{
		data: data,
		cid:  cid,
	}

	if err := l.readHeader(); err != nil {
		return nil, err
	}

	return l, nil
}

func (l *Loader) readHeader() error {
	if len(l.data) < 16 {
		return fmt.Errorf("teehistorian: file too short")
	}

	var uuid [16]byte
	copy(uuid[:], l.data[:16])
	if uuid != HeaderUUID {
		return fmt.Errorf("teehistorian: invalid UUID header")
	}
	l.pos = 16

	jsonEnd := -1
	for i := l.pos; i < len(l.data); i++ {
		if l.data[i] == 0 {
			jsonEnd = i
			break
		}
	}
	if jsonEnd < 0 {
		return fmt.Errorf("teehistorian: unterminated JSON header")
	}

	var hdr HeaderJSON
	if err := json.Unmarshal(l.data[l.pos:jsonEnd], &hdr); err != nil {
		return fmt.Errorf("teehistorian: parse header JSON: %w", err)
	}
	l.pos = jsonEnd + 1

	l.info = replay.RecordingInfo{
		Format:      replay.FormatTeehistorian,
		Map:         hdr.Map,
		SelectedCID: l.cid,
	}

	return nil
}

// NextInput reads messages until an input for the selected CID is found.
func (l *Loader) NextInput() (replay.InputFrame, error) {
	if l.done {
		return replay.InputFrame{}, io.EOF
	}

	for {
		if l.pos >= len(l.data) {
			l.done = true
			return replay.InputFrame{}, io.EOF
		}

		msgID, n := varint.Varint(l.data[l.pos:])
		if n <= 0 {
			l.done = true
			return replay.InputFrame{}, io.EOF
		}
		l.pos += n

		switch {
		case msgID >= 0 && msgID <= MaxDirectPlayerDiff:
			// PLAYER_DIFF: cid=msgID, dx, dy
			if !l.skipVarints(2) {
				return replay.InputFrame{}, io.EOF
			}

		case msgID == MsgFinish:
			l.done = true
			return replay.InputFrame{}, io.EOF

		case msgID == MsgTickSkip:
			dt, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n
			l.tick += dt + 1

		case msgID == MsgPlayerNew:
			cid, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n
			if !l.skipVarints(2) { // x, y
				return replay.InputFrame{}, io.EOF
			}
			if cid >= 0 && cid < MaxClients {
				l.alive[cid] = true
				if l.cid == -1 {
					l.cid = cid
					l.info.SelectedCID = cid
				}
			}

		case msgID == MsgPlayerOld:
			cid, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n
			if cid >= 0 && cid < MaxClients {
				l.alive[cid] = false
				l.hasInput[cid] = false
			}

		case msgID == MsgInputNew:
			cid, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n

			var input [packet.InputFields]int
			for i := range packet.InputFields {
				v, n := varint.Varint(l.data[l.pos:])
				if n <= 0 {
					l.done = true
					return replay.InputFrame{}, io.EOF
				}
				l.pos += n
				input[i] = v
			}

			if cid >= 0 && cid < MaxClients {
				l.inputs[cid] = input
				l.hasInput[cid] = true
			}
			l.tick++

			if cid == l.cid {
				return replay.InputFrame{
					Tick:  l.tick,
					Input: inputArrayToPlayerInput(input),
				}, nil
			}

		case msgID == MsgInputDiff:
			cid, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n

			var diff [packet.InputFields]int
			for i := range packet.InputFields {
				v, n := varint.Varint(l.data[l.pos:])
				if n <= 0 {
					l.done = true
					return replay.InputFrame{}, io.EOF
				}
				l.pos += n
				diff[i] = v
			}

			if cid >= 0 && cid < MaxClients {
				for i := range packet.InputFields {
					l.inputs[cid][i] += diff[i]
				}
				l.hasInput[cid] = true
			}
			l.tick++

			if cid == l.cid {
				return replay.InputFrame{
					Tick:  l.tick,
					Input: inputArrayToPlayerInput(l.inputs[cid]),
				}, nil
			}

		case msgID == MsgMessage:
			// cid, msgsize, raw data
			_, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n
			msgSize, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n
			if msgSize < 0 || l.pos+msgSize > len(l.data) {
				l.done = true
				return replay.InputFrame{}, fmt.Errorf("teehistorian: invalid message size %d", msgSize)
			}
			l.pos += msgSize

		case msgID == MsgJoin:
			if !l.skipVarints(1) {
				return replay.InputFrame{}, io.EOF
			}

		case msgID == MsgDrop:
			if !l.skipVarints(1) { // cid
				return replay.InputFrame{}, io.EOF
			}
			if err := l.skipString(); err != nil {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}

		case msgID == MsgConsoleCommand:
			if !l.skipVarints(2) { // cid, flags
				return replay.InputFrame{}, io.EOF
			}
			if err := l.skipString(); err != nil { // cmd
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			numArgs, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n
			for range numArgs {
				if err := l.skipString(); err != nil {
					l.done = true
					return replay.InputFrame{}, io.EOF
				}
			}

		case msgID == MsgEx:
			if l.pos+16 > len(l.data) {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += 16 // uuid
			exSize, n := varint.Varint(l.data[l.pos:])
			if n <= 0 {
				l.done = true
				return replay.InputFrame{}, io.EOF
			}
			l.pos += n
			if exSize < 0 || l.pos+exSize > len(l.data) {
				l.done = true
				return replay.InputFrame{}, fmt.Errorf("teehistorian: invalid EX size %d", exSize)
			}
			l.pos += exSize

		default:
			l.done = true
			return replay.InputFrame{}, fmt.Errorf("teehistorian: unknown message ID %d at offset %d", msgID, l.pos)
		}
	}
}

func (l *Loader) skipVarints(count int) bool {
	for range count {
		_, n := varint.Varint(l.data[l.pos:])
		if n <= 0 {
			l.done = true
			return false
		}
		l.pos += n
	}
	return true
}

func (l *Loader) skipString() error {
	for l.pos < len(l.data) {
		if l.data[l.pos] == 0 {
			l.pos++
			return nil
		}
		l.pos++
	}
	return fmt.Errorf("teehistorian: unterminated string")
}

// Info returns metadata about the recording.
func (l *Loader) Info() replay.RecordingInfo {
	return l.info
}

// Close is a no-op (data was read into memory in Open).
func (l *Loader) Close() error {
	return nil
}

// CIDs returns the sorted list of client IDs that have at least one
// INPUT_NEW or INPUT_DIFF message in the file. This performs a full
// scan of the message stream (without modifying the loader's read position).
func (l *Loader) CIDs() []int {
	seen := make(map[int]bool)
	pos := l.headerEnd()

	for pos < len(l.data) {
		msgID, n := varint.Varint(l.data[pos:])
		if n <= 0 {
			break
		}
		pos += n

		switch {
		case msgID >= 0 && msgID <= MaxDirectPlayerDiff:
			skipVarints(&pos, l.data, 2)

		case msgID == MsgFinish:
			break

		case msgID == MsgTickSkip:
			skipVarints(&pos, l.data, 1)

		case msgID == MsgPlayerNew:
			skipVarints(&pos, l.data, 3) // cid, x, y

		case msgID == MsgPlayerOld:
			skipVarints(&pos, l.data, 1)

		case msgID == MsgInputNew, msgID == MsgInputDiff:
			cid, cn := varint.Varint(l.data[pos:])
			if cn <= 0 {
				break
			}
			pos += cn
			if cid >= 0 && cid < MaxClients {
				seen[cid] = true
			}
			skipVarints(&pos, l.data, packet.InputFields) // 10 input fields

		case msgID == MsgMessage:
			skipVarints(&pos, l.data, 1) // cid
			msgSize, sn := varint.Varint(l.data[pos:])
			if sn <= 0 {
				break
			}
			pos += sn
			if msgSize >= 0 && pos+msgSize <= len(l.data) {
				pos += msgSize
			}

		case msgID == MsgJoin:
			skipVarints(&pos, l.data, 1)

		case msgID == MsgDrop:
			skipVarints(&pos, l.data, 1)
			skipString(&pos, l.data)

		case msgID == MsgConsoleCommand:
			skipVarints(&pos, l.data, 2) // cid, flags
			skipString(&pos, l.data)     // cmd
			numArgs, an := varint.Varint(l.data[pos:])
			if an <= 0 {
				break
			}
			pos += an
			for range numArgs {
				skipString(&pos, l.data)
			}

		case msgID == MsgEx:
			if pos+16 > len(l.data) {
				return sortedKeys(seen)
			}
			pos += 16 // uuid
			exSize, en := varint.Varint(l.data[pos:])
			if en <= 0 {
				break
			}
			pos += en
			if exSize >= 0 && pos+exSize <= len(l.data) {
				pos += exSize
			}

		default:
			return sortedKeys(seen)
		}
	}

	return sortedKeys(seen)
}

// headerEnd returns the byte offset just past the header (UUID + JSON + null terminator).
func (l *Loader) headerEnd() int {
	pos := 16 // skip UUID
	for pos < len(l.data) {
		if l.data[pos] == 0 {
			return pos + 1
		}
		pos++
	}
	return pos
}

func skipVarints(pos *int, data []byte, count int) {
	for range count {
		if *pos >= len(data) {
			return
		}
		_, n := varint.Varint(data[*pos:])
		if n <= 0 {
			return
		}
		*pos += n
	}
}

func skipString(pos *int, data []byte) {
	for *pos < len(data) {
		if data[*pos] == 0 {
			*pos++
			return
		}
		*pos++
	}
}

func sortedKeys(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort (max 64 elements).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

func inputArrayToPlayerInput(a [packet.InputFields]int) packet.PlayerInput {
	return packet.UnsafePlayerInputFromRaw(a)
}
