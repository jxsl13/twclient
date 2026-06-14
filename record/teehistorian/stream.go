package teehistorian

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jxsl13/twclient/packet"
)

// Decoder reads a teehistorian file incrementally off an io.Reader, yielding one
// Record at a time without ever buffering the whole body. It is the streaming
// counterpart to the in-memory Parse/File path: the header is parsed eagerly in
// NewDecoder, then each Next call decodes exactly one body record, pulling only
// the bytes that record needs from the underlying reader on demand. Variable
// length sub-fields (Message.Msg, Ex.Data, Drop/ConsoleCommand strings) are
// copied into the returned record, so every record owns its bytes and stays
// valid past the following Next call.
type Decoder struct {
	r    *bufio.Reader
	hdr  Header
	done bool
}

// NewDecoder reads and verifies the Magic, reads the NUL-terminated JSON header,
// and decodes the Header (raw bytes plus the best-effort typed view). It does
// NOT read the body — that happens incrementally via Next.
func NewDecoder(r io.Reader) (*Decoder, error) {
	br := bufio.NewReader(r)
	var magic [16]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return nil, fmt.Errorf("teehistorian: read magic: %w", err)
	}
	if !bytes.Equal(magic[:], Magic[:]) {
		return nil, errors.New("teehistorian: bad magic")
	}
	hdr, err := br.ReadBytes(0)
	if err != nil {
		return nil, fmt.Errorf("teehistorian: read header: %w", err)
	}
	hdrJSON := hdr[:len(hdr)-1] // drop NUL terminator
	d := &Decoder{r: br}
	d.hdr.Raw = append(json.RawMessage(nil), hdrJSON...)
	_ = json.Unmarshal(hdrJSON, &d.hdr) // typed view best-effort; Raw is authoritative
	return d, nil
}

// Header returns the decoded header (available immediately after NewDecoder).
func (d *Decoder) Header() Header { return d.hdr }

// Next decodes exactly one body record, reading only the bytes that record
// needs. It returns io.EOF at the clean end of the stream (no more records, or
// just past a Finish marker — mirroring parseBody's break-on-Finish).
func (d *Decoder) Next() (Record, error) {
	if d.done {
		return nil, io.EOF
	}
	n, err := readVarint(d.r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			d.done = true
			return nil, io.EOF // clean end of body
		}
		return nil, fmt.Errorf("teehistorian: read marker: %w", err)
	}
	rec, err := d.decodeRecord(n)
	if err != nil {
		return nil, err
	}
	if _, ok := rec.(Finish); ok {
		d.done = true
	}
	return rec, nil
}

// decodeRecord reconstructs one record from its leading int, mirroring
// readRecord but pulling bytes incrementally from the reader.
func (d *Decoder) decodeRecord(n int) (Record, error) {
	if n >= 0 {
		dx, dy, err := d.readInt2()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: player_diff: %w", err)
		}
		return PlayerDiff{Cid: n, Dx: dx, Dy: dy}, nil
	}
	switch Marker(-n) {
	case MarkerFinish:
		return Finish{}, nil
	case MarkerTickSkip:
		dt, err := readVarint(d.r)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: tick_skip: %w", err)
		}
		return TickSkip{Dt: dt}, nil
	case MarkerPlayerNew:
		cid, err := readVarint(d.r)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: player_new: %w", err)
		}
		x, y, err := d.readInt2()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: player_new: %w", err)
		}
		return PlayerNew{Cid: cid, X: x, Y: y}, nil
	case MarkerPlayerOld:
		cid, err := readVarint(d.r)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: player_old: %w", err)
		}
		return PlayerOld{Cid: cid}, nil
	case MarkerInputDiff, MarkerInputNew:
		cid, err := readVarint(d.r) // unique_id is NOT written to the file
		if err != nil {
			return nil, fmt.Errorf("teehistorian: input cid: %w", err)
		}
		var arr [packet.InputFields]int
		for i := range arr {
			arr[i], err = readVarint(d.r)
			if err != nil {
				return nil, fmt.Errorf("teehistorian: input body: %w", err)
			}
		}
		pi := packet.UnsafePlayerInputFromRaw(arr) // no validation: deltas/raw values may be out of range
		if Marker(-n) == MarkerInputNew {
			return InputNew{Cid: cid, Input: pi}, nil
		}
		return InputDiff{Cid: cid, Diff: pi}, nil
	case MarkerMessage:
		cid, size, err := d.readInt2()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: message head: %w", err)
		}
		msg, err := d.readRaw(size)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: message body: %w", err)
		}
		return Message{Cid: cid, Msg: msg}, nil
	case MarkerJoin:
		cid, err := readVarint(d.r)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: join: %w", err)
		}
		return Join{Cid: cid}, nil
	case MarkerDrop:
		cid, err := readVarint(d.r)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: drop cid: %w", err)
		}
		reason, err := d.readString()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: drop reason: %w", err)
		}
		return Drop{Cid: cid, Reason: reason}, nil
	case MarkerConsoleCommand:
		cid, flagMask, err := d.readInt2()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ccmd head: %w", err)
		}
		cmd, err := d.readString()
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ccmd cmd: %w", err)
		}
		num, err := readVarint(d.r)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ccmd numargs: %w", err)
		}
		args := make([]string, num)
		for i := range args {
			args[i], err = d.readString()
			if err != nil {
				return nil, fmt.Errorf("teehistorian: ccmd arg %d: %w", i, err)
			}
		}
		return ConsoleCommand{Cid: cid, FlagMask: flagMask, Cmd: cmd, Args: args}, nil
	case MarkerEx:
		var uuid [16]byte
		if _, err := io.ReadFull(d.r, uuid[:]); err != nil {
			return nil, fmt.Errorf("teehistorian: ex uuid: %w", err)
		}
		size, err := readVarint(d.r)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ex size: %w", err)
		}
		dat, err := d.readRaw(size)
		if err != nil {
			return nil, fmt.Errorf("teehistorian: ex data: %w", err)
		}
		return Ex{UUID: uuid, Data: dat}, nil
	default:
		return nil, fmt.Errorf("teehistorian: unknown marker %d", -n)
	}
}

// readInt2 reads two consecutive varints.
func (d *Decoder) readInt2() (a, b int, err error) {
	if a, err = readVarint(d.r); err != nil {
		return 0, 0, err
	}
	if b, err = readVarint(d.r); err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

// readRaw reads exactly size bytes into a freshly allocated slice the record
// owns. A zero size yields a nil slice (matching Parse's append([]byte(nil),...)),
// so streamed and in-memory records are deep-equal.
func (d *Decoder) readRaw(size int) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("teehistorian: negative length %d", size)
	}
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(d.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// readString reads a NUL-terminated string and copies it into an owned string
// (no sanitization, matching NextStringSanitized(0) on round-trip data).
func (d *Decoder) readString() (string, error) {
	raw, err := d.r.ReadBytes(0)
	if err != nil {
		return "", err
	}
	return string(raw[:len(raw)-1]), nil // drop NUL; string() copies → owned
}

// readVarint decodes one teeworlds variable-length integer, pulling bytes from r
// on demand. It mirrors the packer/varint wire format exactly: the first byte
// carries the sign in bit 6, the continuation flag in bit 7, and 6 data bits;
// each following byte carries 7 data bits plus a continuation flag. A clean EOF
// before any byte is reported as io.EOF; a truncated varint as io.ErrUnexpectedEOF.
func readVarint(r io.ByteReader) (int, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	sign := int((b >> 6) & 1)
	value := int(b & 0x3f)
	if b < 0x80 { // no continuation bit
		return value ^ -sign, nil
	}
	const maxCont = 4 // continuation bytes (MaxVarintLen32 - 1)
	for i := range maxCont {
		b, err = r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, err
		}
		if i == maxCont-1 && b > 0x0f { // 5th byte may hold only 4 data bits
			return 0, errors.New("teehistorian: varint overflows int32")
		}
		value |= int(b&0x7f) << (6 + 7*i)
		if b < 0x80 {
			break
		}
	}
	return value ^ -sign, nil
}

// Encoder writes a teehistorian file incrementally to an io.Writer: NewEncoder
// emits the Magic, raw header bytes and NUL terminator, then each Write appends
// one record's wire bytes. The concatenation is byte-identical to (*File).WriteTo.
type Encoder struct {
	w   io.Writer
	buf []byte // reused scratch for one record's wire bytes
}

// NewEncoder writes the file preamble (Magic + h.Raw + NUL) to w and returns an
// Encoder ready to append body records.
func NewEncoder(w io.Writer, h Header) (*Encoder, error) {
	var head []byte
	head = append(head, Magic[:]...)
	head = append(head, h.Raw...)
	head = append(head, 0) // NUL terminator
	if _, err := w.Write(head); err != nil {
		return nil, err
	}
	return &Encoder{w: w}, nil
}

// Write appends one record's wire bytes to the underlying writer, reusing the
// shared writeRecord encoding so output matches the in-memory path exactly.
func (e *Encoder) Write(rec Record) error {
	e.buf = writeRecord(e.buf[:0], rec)
	_, err := e.w.Write(e.buf)
	return err
}
