// Package ghost reads DDNet .gho (ghost) files — client-side race recordings of
// a single tee's per-tick character state on a map — and writes them back
// unchanged (round-trip).
//
// # Format
//
// A file is a fixed-size binary [Header] (8-byte [Magic] marker, version, owner
// and map names, a map CRC/zeroes word, big-endian tick count and time, and —
// for version 6 — a 32-byte map SHA256) followed by a stream of physical
// chunks. The grammar follows DDNet src/engine/client/ghost.{h,cpp}
// (CGhostHeader, CGhostRecorder, CGhostLoader) and the item layouts in
// src/game/client/components/ghost.h.
//
// Each physical chunk is a 4-byte header [type, numItems, sizeHi, sizeLo]
// followed by sizeHi<<8|sizeLo payload bytes. The payload is huffman-compressed
// (teeworlds network compression); decompressing yields a teeworlds-varint
// (intpack) stream of int32 fields. A chunk holds up to NUM_ITEMS_PER_CHUNK (50)
// fixed-size items of one [DataType]; the first item is stored absolute and each
// following item is a per-field delta against the previous one.
//
// # Chunks
//
// Items are exposed one per [Chunk] in stream order — [Skin], [CharacterNoTick],
// [Character], [StartTick], or [Raw] for unrecognized types — with their fields
// resolved to ABSOLUTE values. The physical chunking is not stored: it is
// recomputed on write exactly as DDNet's CGhostRecorder does (consecutive items
// of the same type, flushed every 50), so [File.WriteTo] reproduces a
// DDNet-written file byte-for-byte.
//
// Usage:
//
//	f, err := ghost.Parse(r)
//	for _, c := range f.Chunks {
//		if ch, ok := c.(ghost.Character); ok {
//			// ch.X, ch.Y, ch.Tick ...
//		}
//	}
package ghost
