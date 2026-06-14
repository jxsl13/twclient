package teehistorian

import "maps"

import "github.com/jxsl13/twclient/packet"

// PlayerState is a player's absolute position at a tick (world units).
type PlayerState struct{ X, Y int }

// Ticks replays the record stream and yields, once per game tick, the absolute
// position of every live player and each player's current input (deltas
// accumulated, implicit ticks reconstructed). It is the dataset source for
// behavior cloning. Return false from yield to stop early.
//
// Tick boundaries are reconstructed from TICK_SKIP markers and the implicit
// advance DDNet uses (player records within a tick are in increasing client-id
// order; a client-id that does not increase starts the next tick).
func (f *File) Ticks(yield func(tick int, players map[int]PlayerState, inputs map[int]packet.PlayerInput) bool) {
	pos := map[int]PlayerState{}
	input := map[int]packet.PlayerInput{}
	tick := 0
	lastCid := -1
	dirty := false

	emit := func() bool {
		ps := make(map[int]PlayerState, len(pos))
		maps.Copy(ps, pos)
		in := make(map[int]packet.PlayerInput, len(input))
		maps.Copy(in, input)
		return yield(tick, ps, in)
	}

	// boundary advances to a new tick, emitting the completed one first.
	boundaryAt := func(cid int) bool {
		if dirty && cid <= lastCid {
			if !emit() {
				return false
			}
			tick++
			dirty = false
		}
		lastCid = cid
		dirty = true
		return true
	}

	for _, rec := range f.Records {
		switch r := rec.(type) {
		case TickSkip:
			if dirty {
				if !emit() {
					return
				}
			}
			tick += r.Dt + 1
			lastCid = -1
			dirty = false
		case PlayerNew:
			if !boundaryAt(r.Cid) {
				return
			}
			pos[r.Cid] = PlayerState{X: r.X, Y: r.Y}
		case PlayerDiff:
			if !boundaryAt(r.Cid) {
				return
			}
			p := pos[r.Cid]
			pos[r.Cid] = PlayerState{X: p.X + r.Dx, Y: p.Y + r.Dy}
		case PlayerOld:
			if !boundaryAt(r.Cid) {
				return
			}
			delete(pos, r.Cid)
		case InputNew:
			input[r.Cid] = r.Input
			dirty = true
		case InputDiff:
			prev := input[r.Cid]
			cur := prev.Raw()
			diff := r.Diff
			d := diff.Raw()
			for i := range cur {
				cur[i] += d[i]
			}
			input[r.Cid] = packet.UnsafePlayerInputFromRaw(cur)
			dirty = true
		case Finish:
			// handled by the trailing flush
		}
	}
	if dirty {
		emit()
	}
}
