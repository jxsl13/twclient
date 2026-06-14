package ghost

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// Decoder reads a ghost file incrementally off an io.Reader, yielding one Chunk
// at a time without buffering the whole file. It is the streaming counterpart to
// Parse/File: the header is parsed eagerly in NewDecoder, then each Next call
// returns the next item. Items are decoded one physical chunk at a time (at most
// NumItemsPerChunk buffered), so memory stays bounded regardless of file size.
type Decoder struct {
	r       *bufio.Reader
	hdr     Header
	pending []Chunk
	idx     int
	done    bool
}

// NewDecoder reads and verifies the header (Magic, version, names, ticks/time
// and — for version 6 — the map SHA256). It does NOT read the body; that happens
// incrementally via Next.
func NewDecoder(r io.Reader) (*Decoder, error) {
	br := bufio.NewReader(r)
	hdr, err := readHeader(br)
	if err != nil {
		return nil, err
	}
	return &Decoder{r: br, hdr: hdr}, nil
}

// readHeader decodes a CGhostHeader from r, reading the base header first and the
// SHA256 separately only for version >= 6 (mirroring CGhostLoader::ReadHeader).
func readHeader(r io.Reader) (Header, error) {
	const base = len(Magic) + 1 + MaxNameLength + MapNameLength + 4 + 4 + 4
	buf := make([]byte, base)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Header{}, fmt.Errorf("ghost: read header: %w", unexpected(err))
	}
	// parseHeader on a version<6 header would also read a SHA256; feed it the
	// base bytes and, when needed, the extra SHA256 bytes.
	if buf[len(Magic)] >= 6 {
		sha := make([]byte, Sha256Size)
		if _, err := io.ReadFull(r, sha); err != nil {
			return Header{}, fmt.Errorf("ghost: read map sha256: %w", unexpected(err))
		}
		buf = append(buf, sha...)
	}
	hdr, _, err := parseHeader(buf)
	return hdr, err
}

// unexpected maps a clean EOF mid-read to io.ErrUnexpectedEOF.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// Header returns the decoded header (available immediately after NewDecoder).
func (d *Decoder) Header() Header { return d.hdr }

// Next returns the next item in stream order, decoding one physical chunk at a
// time on demand. It returns io.EOF once the stream is exhausted.
func (d *Decoder) Next() (Chunk, error) {
	for d.idx >= len(d.pending) {
		if d.done {
			return nil, io.EOF
		}
		if err := d.readChunk(); err != nil {
			return nil, err
		}
	}
	c := d.pending[d.idx]
	d.idx++
	return c, nil
}

// readChunk reads and decodes the next physical chunk into d.pending. A clean EOF
// before any chunk header byte ends the stream; a partial header is an error.
func (d *Decoder) readChunk() error {
	var head [4]byte
	n, err := io.ReadFull(d.r, head[:])
	if err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			d.done = true
			return io.EOF
		}
		return fmt.Errorf("ghost: chunk header: %w", unexpected(err))
	}
	typ := int(head[0])
	numItems := int(head[1])
	size := int(head[2])<<8 | int(head[3])
	if size <= 0 || size > MaxChunkSize {
		return fmt.Errorf("ghost: invalid chunk size %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(d.r, payload); err != nil {
		return fmt.Errorf("ghost: chunk data: %w", unexpected(err))
	}
	chunks, err := decodePhysicalChunk(typ, numItems, payload)
	if err != nil {
		return err
	}
	d.pending = chunks
	d.idx = 0
	return nil
}

// Encoder writes a ghost file incrementally to an io.Writer: NewEncoder emits the
// header, then each Write appends one item, batching consecutive same-type items
// into physical chunks exactly as CGhostRecorder does. Close MUST be called to
// flush the final partial chunk; the resulting bytes are identical to
// (*File).WriteTo.
type Encoder struct {
	w     io.Writer
	typ   int
	items [][]int32
}

// NewEncoder writes the header to w and returns an Encoder ready to append items.
func NewEncoder(w io.Writer, h Header) (*Encoder, error) {
	if _, err := w.Write(appendHeader(nil, h)); err != nil {
		return nil, err
	}
	return &Encoder{w: w}, nil
}

// Write appends one item. A pending physical chunk is flushed first if this item
// changes type or the chunk is already full (NumItemsPerChunk items).
func (e *Encoder) Write(c Chunk) error {
	t := c.dataType()
	if len(e.items) > 0 && (t != e.typ || len(e.items) >= NumItemsPerChunk) {
		if err := e.flush(); err != nil {
			return err
		}
	}
	e.typ = t
	e.items = append(e.items, c.fields())
	return nil
}

// Close flushes the final pending chunk. It must be called exactly once after
// the last Write; it does not close the underlying writer.
func (e *Encoder) Close() error { return e.flush() }

func (e *Encoder) flush() error {
	if len(e.items) == 0 {
		return nil
	}
	b, err := appendPhysicalChunk(nil, e.typ, e.items)
	if err != nil {
		return err
	}
	e.items = e.items[:0]
	_, err = e.w.Write(b)
	return err
}
