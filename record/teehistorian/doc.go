// Package teehistorian reads DDNet .teehistorian files — server-side recordings
// of every player's input and position per tick — and writes them back unchanged
// (round-trip). Its purpose is to extract (game-state, input) pairs from human
// play for machine-learning behavior cloning.
//
// # Format
//
// A file is a 16-byte UUID magic ([Magic]), a NUL-terminated JSON header, then a
// stream of teeworlds-varint items. The grammar follows DDNet
// src/game/server/teehistorian.{h,cpp} (version "2") and libtw2
// doc/teehistorian.md: a non-negative leading int is a player position delta;
// a negative int is a -TEEHISTORIAN_* marker (tick skip, player new/old, input
// new/diff, message, join, drop, console command, extension chunk, finish).
//
// # Round-trip
//
// [Parse] keeps records in exact stream order with their raw wire fields, so
// [File.WriteTo] reproduces the input byte-for-byte. Positions and inputs are
// delta-encoded on the wire; the deltas, and the implicit tick stream, are
// decoded to ABSOLUTE per-tick values on demand by [File.Ticks] — the dataset
// source for behavior cloning.
//
// Usage:
//
//	f, err := teehistorian.Parse(r)
//	f.Ticks(func(tick int, players map[int]teehistorian.PlayerState, inputs map[int][10]int) bool {
//		// each player's absolute position + current input at this tick
//		return true
//	})
package teehistorian
