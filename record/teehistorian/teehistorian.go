package teehistorian

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// Magic is the 16-byte file identifier (CalculateUUID("teehistorian@ddnet.tw")).
var Magic = packer.CalculateUUID("teehistorian@ddnet.tw")

// Marker is a teehistorian body-item type. On the wire it is stored NEGATED
// (e.g. -MarkerTickSkip); a non-negative leading int is a player position diff
// instead of a marker.
type Marker int

// The TEEHISTORIAN_* body-item markers (DDNet teehistorian.cpp).
const (
	MarkerFinish         Marker = 1
	MarkerTickSkip       Marker = 2
	MarkerPlayerNew      Marker = 3
	MarkerPlayerOld      Marker = 4
	MarkerInputDiff      Marker = 5
	MarkerInputNew       Marker = 6
	MarkerMessage        Marker = 7
	MarkerJoin           Marker = 8
	MarkerDrop           Marker = 9
	MarkerConsoleCommand Marker = 10
	MarkerEx             Marker = 11
)

// Header is the decoded JSON header. Raw holds the exact original JSON bytes so
// WriteTo reproduces them verbatim; the typed fields are a convenience view.
type Header struct {
	Comment         string `json:"comment"`
	Version         string `json:"version"`
	VersionMinor    string `json:"version_minor"`
	GameUUID        string `json:"game_uuid"`
	ServerVersion   string `json:"server_version"`
	StartTime       string `json:"start_time"`
	ServerName      string `json:"server_name"`
	ServerPort      int    `json:"server_port"`
	GameType        string `json:"game_type"`
	MapName         string `json:"map_name"`
	MapSize         int    `json:"map_size"`
	MapSha256       string `json:"map_sha256"`
	MapCrc          string `json:"map_crc"`
	PrngDescription string `json:"prng_description"`
	PrevGameUUID    string `json:"prev_game_uuid,omitempty"`

	Raw json.RawMessage `json:"-"` // original header bytes (source of truth for write-back)
}

// File is a parsed teehistorian file.
type File struct {
	Header  Header
	Records []Record
}

// Record is one body item, holding raw wire-form fields.
type Record interface{ isRecord() }

type (
	// TickSkip advances the tick by Dt+1 (TEEHISTORIAN_TICK_SKIP).
	TickSkip struct{ Dt int }
	// PlayerNew is a player's first/respawn absolute position.
	PlayerNew struct{ Cid, X, Y int }
	// PlayerOld marks a player gone/dead.
	PlayerOld struct{ Cid int }
	// PlayerDiff is a player position delta vs its previous (dx, dy).
	PlayerDiff struct{ Cid, Dx, Dy int }
	// InputNew is a full absolute input. (UniqueClientId is used server-side to
	// pick diff-vs-new but is NOT written to the file.)
	InputNew struct {
		Cid   int
		Input packet.PlayerInput
	}
	// InputDiff is an input delta vs the player's previous: each field holds the
	// per-field delta (0 = unchanged), in the same layout as packet.PlayerInput.
	InputDiff struct {
		Cid  int
		Diff packet.PlayerInput
	}
	// Message is a raw game/system message from a player.
	Message struct {
		Cid int
		Msg []byte
	}
	// Join / Drop player lifecycle.
	Join struct{ Cid int }
	Drop struct {
		Cid    int
		Reason string
	}
	// ConsoleCommand is an executed rcon/chat command.
	ConsoleCommand struct {
		Cid, FlagMask int
		Cmd           string
		Args          []string
	}
	// Ex is an extension chunk keyed by UUID; unknown UUIDs are preserved verbatim.
	Ex struct {
		UUID [16]byte
		Data []byte
	}
	// Finish marks the end of the file.
	Finish struct{}
)

func (TickSkip) isRecord()       {}
func (PlayerNew) isRecord()      {}
func (PlayerOld) isRecord()      {}
func (PlayerDiff) isRecord()     {}
func (InputNew) isRecord()       {}
func (InputDiff) isRecord()      {}
func (Message) isRecord()        {}
func (Join) isRecord()           {}
func (Drop) isRecord()           {}
func (ConsoleCommand) isRecord() {}
func (Ex) isRecord()             {}
func (Finish) isRecord()         {}

// Parse reads a whole teehistorian file.
func Parse(r io.Reader) (*File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("teehistorian: read: %w", err)
	}
	if len(data) < len(Magic) {
		return nil, errors.New("teehistorian: too short for magic")
	}
	if !bytes.Equal(data[:len(Magic)], Magic[:]) {
		return nil, errors.New("teehistorian: bad magic")
	}
	rest := data[len(Magic):]

	nul := bytes.IndexByte(rest, 0)
	if nul < 0 {
		return nil, errors.New("teehistorian: unterminated JSON header")
	}
	hdrJSON := rest[:nul]
	body := rest[nul+1:]

	f := &File{}
	f.Header.Raw = append(json.RawMessage(nil), hdrJSON...)
	_ = json.Unmarshal(hdrJSON, &f.Header) // typed view is best-effort; Raw is authoritative

	if err := f.parseBody(body); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *File) parseBody(body []byte) error {
	u := packer.NewUnpacker(body)
	for u.RemainingSize() > 0 {
		n, err := u.NextInt()
		if err != nil {
			return fmt.Errorf("teehistorian: read marker: %w", err)
		}
		rec, err := readRecord(u, n)
		if err != nil {
			return err
		}
		f.Records = append(f.Records, rec)
		if _, ok := rec.(Finish); ok {
			break
		}
	}
	return nil
}

// ints reads count varints.
func ints(u *packer.Unpacker, count int) ([]int, error) {
	out := make([]int, count)
	for i := range out {
		v, err := u.NextInt()
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func readRecord(u *packer.Unpacker, n int) (Record, error) {
	if n >= 0 {
		v, err := ints(u, 2) // dx, dy
		if err != nil {
			return nil, fmt.Errorf("teehistorian: player_diff: %w", err)
		}
		return PlayerDiff{Cid: n, Dx: v[0], Dy: v[1]}, nil
	}
	switch Marker(-n) {
	case MarkerFinish:
		return Finish{}, nil
	case MarkerTickSkip:
		dt, err := u.NextInt()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: tick_skip: %w", err)
		}
		return TickSkip{Dt: dt}, nil
	case MarkerPlayerNew:
		v, err := ints(u, 3)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: player_new: %w", err)
		}
		return PlayerNew{Cid: v[0], X: v[1], Y: v[2]}, nil
	case MarkerPlayerOld:
		cid, err := u.NextInt()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: player_old: %w", err)
		}
		return PlayerOld{Cid: cid}, nil
	case MarkerInputDiff, MarkerInputNew:
		cid, err := u.NextInt() // unique_id is NOT written to the file
		if err != nil {
			return nil, fmt.Errorf("teehistorian: input cid: %w", err)
		}
		in, err := ints(u, packet.InputFields)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: input body: %w", err)
		}
		var arr [packet.InputFields]int
		copy(arr[:], in)
		pi := packet.UnsafePlayerInputFromRaw(arr) // no validation: deltas/raw values may be out of range
		if Marker(-n) == MarkerInputNew {
			return InputNew{Cid: cid, Input: pi}, nil
		}
		return InputDiff{Cid: cid, Diff: pi}, nil
	case MarkerMessage:
		head, err := ints(u, 2) // cid, size
		if err != nil {
			return nil, fmt.Errorf("teehistorian: message head: %w", err)
		}
		raw, err := u.NextRaw(head[1])
		if err != nil {
			return nil, fmt.Errorf("teehistorian: message body: %w", err)
		}
		return Message{Cid: head[0], Msg: append([]byte(nil), raw...)}, nil
	case MarkerJoin:
		cid, err := u.NextInt()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: join: %w", err)
		}
		return Join{Cid: cid}, nil
	case MarkerDrop:
		cid, err := u.NextInt()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: drop cid: %w", err)
		}
		reason, err := u.NextStringSanitized(0) // raw, unmodified (round-trip)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: drop reason: %w", err)
		}
		return Drop{Cid: cid, Reason: reason}, nil
	case MarkerConsoleCommand:
		head, err := ints(u, 2) // cid, flag_mask
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ccmd head: %w", err)
		}
		cmd, err := u.NextStringSanitized(0)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ccmd cmd: %w", err)
		}
		num, err := u.NextInt()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ccmd numargs: %w", err)
		}
		args := make([]string, num)
		for i := range args {
			args[i], err = u.NextStringSanitized(0)
			if err != nil {
				return nil, fmt.Errorf("teehistorian: ccmd arg %d: %w", i, err)
			}
		}
		return ConsoleCommand{Cid: head[0], FlagMask: head[1], Cmd: cmd, Args: args}, nil
	case MarkerEx:
		raw, err := u.NextRaw(len(Magic))
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ex uuid: %w", err)
		}
		var uuid [16]byte
		copy(uuid[:], raw)
		size, err := u.NextInt()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ex size: %w", err)
		}
		dat, err := u.NextRaw(size)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ex data: %w", err)
		}
		return Ex{UUID: uuid, Data: append([]byte(nil), dat...)}, nil
	default:
		return nil, fmt.Errorf("teehistorian: unknown marker %d", -n)
	}
}

// WriteTo re-serializes the file, byte-identical to a canonically-encoded input
// (magic + raw JSON header + NUL + re-packed body in record order).
func (f *File) WriteTo(w io.Writer) (int64, error) {
	var b []byte
	b = append(b, Magic[:]...)
	b = append(b, f.Header.Raw...)
	b = append(b, 0) // NUL terminator
	for _, rec := range f.Records {
		b = writeRecord(b, rec)
	}
	n, err := w.Write(b)
	return int64(n), err
}

// appendMarker writes a marker as its negated int.
func appendMarker(b []byte, m Marker) []byte { return packer.AppendInt(b, -int(m)) }

// appendInput writes the 10 input fields in protocol order.
func appendInput(b []byte, in packet.PlayerInput) []byte {
	for _, v := range in.Raw() {
		b = packer.AppendInt(b, v)
	}
	return b
}

func writeRecord(b []byte, rec Record) []byte {
	switch r := rec.(type) {
	case PlayerDiff:
		b = packer.AppendInt(b, r.Cid)
		b = packer.AppendInt(b, r.Dx)
		b = packer.AppendInt(b, r.Dy)
	case TickSkip:
		b = appendMarker(b, MarkerTickSkip)
		b = packer.AppendInt(b, r.Dt)
	case PlayerNew:
		b = appendMarker(b, MarkerPlayerNew)
		b = packer.AppendInt(b, r.Cid)
		b = packer.AppendInt(b, r.X)
		b = packer.AppendInt(b, r.Y)
	case PlayerOld:
		b = appendMarker(b, MarkerPlayerOld)
		b = packer.AppendInt(b, r.Cid)
	case InputNew:
		b = appendMarker(b, MarkerInputNew)
		b = packer.AppendInt(b, r.Cid)
		b = appendInput(b, r.Input)
	case InputDiff:
		b = appendMarker(b, MarkerInputDiff)
		b = packer.AppendInt(b, r.Cid)
		b = appendInput(b, r.Diff)
	case Message:
		b = appendMarker(b, MarkerMessage)
		b = packer.AppendInt(b, r.Cid)
		b = packer.AppendInt(b, len(r.Msg))
		b = append(b, r.Msg...)
	case Join:
		b = appendMarker(b, MarkerJoin)
		b = packer.AppendInt(b, r.Cid)
	case Drop:
		b = appendMarker(b, MarkerDrop)
		b = packer.AppendInt(b, r.Cid)
		b = packer.AppendStr(b, r.Reason)
	case ConsoleCommand:
		b = appendMarker(b, MarkerConsoleCommand)
		b = packer.AppendInt(b, r.Cid)
		b = packer.AppendInt(b, r.FlagMask)
		b = packer.AppendStr(b, r.Cmd)
		b = packer.AppendInt(b, len(r.Args))
		for _, a := range r.Args {
			b = packer.AppendStr(b, a)
		}
	case Ex:
		b = appendMarker(b, MarkerEx)
		b = append(b, r.UUID[:]...)
		b = packer.AppendInt(b, len(r.Data))
		b = append(b, r.Data...)
	case Finish:
		b = appendMarker(b, MarkerFinish)
	}
	return b
}
