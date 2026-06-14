package demo

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Decoder reads a demo file incrementally off an io.Reader, yielding one Chunk at
// a time without ever buffering the chunk stream. It is the streaming counterpart
// to Parse/File: the preamble (header, timeline markers, SHA256 extension and the
// embedded map) is read eagerly in NewDecoder, then each Next call decodes exactly
// one chunk, pulling only the bytes that chunk needs. Each returned chunk owns its
// payload bytes, so it stays valid past the following Next call.
type Decoder struct {
	r        *bufio.Reader
	hdr      Header
	prevTick int
	done     bool
}

// NewDecoder reads and decodes the whole preamble (everything before the chunk
// stream) and returns a Decoder positioned at the first chunk. It does NOT read
// any chunks — that happens incrementally via Next.
func NewDecoder(r io.Reader) (*Decoder, error) {
	br := bufio.NewReader(r)

	head := make([]byte, headerSize)
	if _, err := io.ReadFull(br, head); err != nil {
		return nil, fmt.Errorf("demo: read header: %w", err)
	}
	h, err := parseFixedHeader(head)
	if err != nil {
		return nil, err
	}

	if int(h.Version) > versionOld {
		tl := make([]byte, timelineSize)
		if _, err := io.ReadFull(br, tl); err != nil {
			return nil, fmt.Errorf("demo: read timeline markers: %w", err)
		}
		h.TimelineMarkers = parseTimeline(tl)
	}

	if int(h.Version) >= versionSha256 {
		peek, err := br.Peek(16)
		if err == nil && bytes.Equal(peek, sha256ExtensionUUID[:]) {
			if _, err := br.Discard(16); err != nil {
				return nil, fmt.Errorf("demo: skip SHA256 extension uuid: %w", err)
			}
			var d [32]byte
			if _, err := io.ReadFull(br, d[:]); err != nil {
				return nil, fmt.Errorf("demo: read SHA256 digest: %w", err)
			}
			h.Sha256 = &d
		}
		// Otherwise the bytes belong to the map data (DDNet rewinds).
	}

	// Stream the map data so a hostile MapSize cannot force a huge allocation.
	var mapBuf bytes.Buffer
	if _, err := io.CopyN(&mapBuf, br, int64(h.MapSize)); err != nil {
		return nil, fmt.Errorf("demo: read map data: %w", err)
	}
	h.MapData = mapBuf.Bytes()

	return &Decoder{r: br, hdr: h, prevTick: -1}, nil
}

// Header returns the decoded header (available immediately after NewDecoder).
func (d *Decoder) Header() Header { return d.hdr }

// Next decodes exactly one chunk, reading only the bytes that chunk needs. It
// returns io.EOF at the clean end of the chunk stream.
func (d *Decoder) Next() (Chunk, error) {
	if d.done {
		return nil, io.EOF
	}
	b, err := d.r.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			d.done = true
			return nil, io.EOF
		}
		return nil, fmt.Errorf("demo: read chunk header: %w", err)
	}
	return d.decodeChunk(b)
}

// decodeChunk reconstructs one chunk from its leading byte, mirroring readChunk
// but pulling bytes incrementally from the reader.
func (d *Decoder) decodeChunk(b byte) (Chunk, error) {
	if b&chunkTypeFlagTickmarker != 0 {
		keyframe := b&chunkTickFlagKeyframe != 0
		delta, absolute := tickDelta(b, d.hdr.Version)
		if absolute {
			var raw [4]byte
			if _, err := io.ReadFull(d.r, raw[:]); err != nil {
				return nil, fmt.Errorf("demo: tick marker: %w", err)
			}
			tick := int(int32(binary.BigEndian.Uint32(raw[:])))
			d.prevTick = tick
			return TickMarker{Tick: tick, Keyframe: keyframe}, nil
		}
		if d.prevTick < 0 {
			return nil, errors.New("demo: tick delta before initial tick")
		}
		tick := d.prevTick + delta
		d.prevTick = tick
		return TickMarker{Tick: tick, Keyframe: keyframe}, nil
	}

	typ := ChunkType((int(b) & chunkMaskType) >> 5)
	size := int(b) & chunkMaskSize
	switch size {
	case 30:
		s, err := d.r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("demo: chunk size: %w", err)
		}
		size = int(s)
	case 31:
		var s [2]byte
		if _, err := io.ReadFull(d.r, s[:]); err != nil {
			return nil, fmt.Errorf("demo: chunk size: %w", err)
		}
		size = int(s[1])<<8 | int(s[0])
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(d.r, payload); err != nil {
		return nil, fmt.Errorf("demo: chunk payload: %w", err)
	}
	return decompressDataChunk(typ, payload)
}

// Encoder writes a demo file incrementally to an io.Writer: NewEncoder emits the
// whole preamble, then each Write appends one chunk's wire bytes. The result is
// byte-identical to (*File).WriteTo.
type Encoder struct {
	w        io.Writer
	prevTick int
	buf      []byte // reused scratch for one chunk's wire bytes
}

// NewEncoder writes the preamble (header + timeline + SHA256 ext + map) to w and
// returns an Encoder ready to append chunks.
func NewEncoder(w io.Writer, h Header) (*Encoder, error) {
	if _, err := w.Write(h.marshalPreamble()); err != nil {
		return nil, err
	}
	return &Encoder{w: w, prevTick: -1}, nil
}

// Write appends one chunk's wire bytes to the underlying writer, reusing the
// shared appendChunk encoding so output matches the in-memory path exactly.
func (e *Encoder) Write(ch Chunk) error {
	buf, err := appendChunk(e.buf[:0], ch, &e.prevTick)
	if err != nil {
		return err
	}
	e.buf = buf
	_, err = e.w.Write(e.buf)
	return err
}
