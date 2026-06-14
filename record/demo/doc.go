// Package demo reads DDNet .demo files — client/server recordings of a match as
// a stream of per-tick snapshots, snapshot deltas and network messages — and
// writes them back unchanged (byte-for-byte round-trip).
//
// # Format
//
// A file is a fixed [CDemoHeader] (7-byte [Magic] "TWDEMO\0", a version byte,
// NUL-padded netversion/mapname/type/timestamp strings and big-endian
// mapsize/mapcrc/length), followed — for demo versions above 3 — by a
// CTimelineMarkers block (count + 64 big-endian tick markers). Version 6 and
// above additionally embed a SHA256 extension (a fixed UUID + 32-byte digest)
// when present. Then comes the raw embedded map (mapsize bytes) and finally the
// chunk stream. The grammar follows DDNet src/engine/demo.h and
// src/engine/shared/demo.cpp (current version is [Version] = 6).
//
// Each chunk begins with one leading byte. If bit 7 ([chunkTypeFlagTickmarker])
// is set it is a [TickMarker]: either an absolute 4-byte big-endian tick (used
// for the first marker, keyframes, or deltas above 31) or a single-byte
// compressed delta relative to the previous marker. Otherwise it is a
// [DataChunk] whose type (snapshot, delta or message) and payload size are
// packed into the leading byte (with 1- or 2-byte size extensions). A data
// chunk's payload is huffman-compressed teeworlds-varint data; snapshots are
// stored as deltas against the previous snapshot.
//
// # Round-trip
//
// [Parse] keeps chunks in exact stream order, holding each data chunk's payload
// in its original compressed wire form, so [File.WriteTo] reproduces the input
// byte-for-byte. Tick-marker wire encoding (absolute vs compressed) is recomputed
// from the absolute tick values exactly as DDNet's recorder does, so it too is
// reproduced exactly. [DataChunk.Decompress] and [DataChunk.Ints] expose the
// huffman/varint-decoded payload on demand for callers that want the contents.
//
// # Streaming
//
// [NewDecoder]/[Decoder.Next] yield one chunk at a time without buffering the
// chunk stream, and [NewEncoder]/[Encoder.Write] re-emit a byte-identical file.
package demo
