package packet

import (
	"fmt"
	"sync"

	"github.com/jxsl13/twclient/packer"
)

// deltaScratch holds the transient, per-call working buffers of applyDelta. It
// is pooled so the hot snapshot path allocates almost nothing per tick: the
// base/deleted lookup maps, the unpacker, and the delta-field buffer are reused
// across calls. Only buffers retained in the resulting Snapshot (item Fields)
// are freshly allocated. Reused via sync.Pool — safe under many concurrent
// clients since each applyDelta call holds its scratch exclusively.
type deltaScratch struct {
	base    map[itemKey][]int
	deleted map[itemKey]bool
	idx     map[itemKey]int // result-item index for O(1) updated-item lookup (V50)
	u       packer.Unpacker
	df      []int
	flat    []int     // accumulates all updated items' absolute fields (one backing alloc, V51)
	upds    []updMeta // per-updated-item metadata, reused across calls
}

// updMeta records where an updated item's absolute fields live in the flat
// scratch buffer so the final single backing slice can be sub-sliced per item.
type updMeta struct {
	typeID, id, off, size int
}

var scratchPool = sync.Pool{
	New: func() any {
		return &deltaScratch{
			base:    make(map[itemKey][]int),
			deleted: make(map[itemKey]bool),
			idx:     make(map[itemKey]int),
		}
	},
}

// deltaBuf returns a reusable []int of length n from the scratch.
func (s *deltaScratch) deltaBuf(n int) []int {
	if cap(s.df) < n {
		s.df = make([]int, n)
	}
	return s.df[:n]
}

// SnapItem represents a single snapshot item with absolute field values.
type SnapItem struct {
	TypeID int
	ID     int
	Fields []int
}

// Snapshot holds the state of one game tick after delta decompression.
type Snapshot struct {
	Tick  int
	Items []SnapItem
}

// SnapStorage maintains a ring buffer of recent snapshots for delta decompression.
type SnapStorage struct {
	Snaps      map[int]*Snapshot
	MaxSnaps   int
	LastTick   int
	ItemSizeFn func(typeID int) int // returns field count or -1; nil → always read from stream
}

// DefaultMaxSnaps is the retained-snapshot window used when none is configured.
// MinMaxSnaps is the floor a configured window is clamped up to, so the
// delta-decompression base (prev + current + slack) is always retained (V53).
const (
	DefaultMaxSnaps = 16
	MinMaxSnaps     = 3
)

// SnapStorageOption configures a SnapStorage at construction time.
type SnapStorageOption func(*SnapStorage)

// WithMaxSnaps sets the retained-snapshot window (MaxSnaps). Invalid input is
// clamped so a partial/bad value cannot break delta decoding (V41, V53):
// n <= 0 falls back to the default (16); 0 < n < MinMaxSnaps is raised to
// MinMaxSnaps so the server's delta base is never purged out from under us.
func WithMaxSnaps(n int) SnapStorageOption {
	return func(ss *SnapStorage) {
		switch {
		case n <= 0:
			ss.MaxSnaps = DefaultMaxSnaps
		case n < MinMaxSnaps:
			ss.MaxSnaps = MinMaxSnaps
		default:
			ss.MaxSnaps = n
		}
	}
}

// NewSnapStorage creates a new snap storage with the given item size function.
// Pass nil to always read sizes from the stream.
//
// MaxSnaps bounds the retained snapshot history (the delta-decompression base
// pool). The server deltas against the last snapshot the client acked, which is
// always recent, so only a small window is needed. 32 ticks (~0.64s) covers
// normal ack lag; keeping it small matters at scale — every retained snapshot
// holds a full copy of all item field slices, so a large window multiplied by
// many concurrent clients dominates heap (see applyDelta allocations).
//
// The window defaults to 16; override it with WithMaxSnaps (V53). Options are
// validated by their constructor, so a bad value never reaches MaxSnaps (V41).
func NewSnapStorage(itemSizeFn func(typeID int) int, opts ...SnapStorageOption) *SnapStorage {
	ss := &SnapStorage{
		Snaps:      make(map[int]*Snapshot),
		MaxSnaps:   DefaultMaxSnaps,
		ItemSizeFn: itemSizeFn,
	}
	for _, opt := range opts {
		if opt != nil { // a nil option is ignored (V70)
			opt(ss)
		}
	}
	return ss
}

// ProcessSnap unpacks a snapshot payload, applies the delta to the base snapshot
// and stores the result. Returns the decoded snapshot.
func (ss *SnapStorage) ProcessSnap(tick, deltaTick int, data []byte) (*Snapshot, error) {
	var base *Snapshot
	baseTick := tick - deltaTick
	if deltaTick >= 0 {
		base = ss.Snaps[baseTick]
	}
	if base == nil {
		base = &Snapshot{Tick: baseTick}
	}

	snap, err := applyDelta(base, tick, data, ss.ItemSizeFn)
	if err != nil {
		return nil, fmt.Errorf("snap: apply delta: %w", err)
	}

	ss.Snaps[tick] = snap
	ss.LastTick = tick

	// Purge snapshots older than the delta base: the server only deltas against
	// a recently acked snapshot, so anything older is dead weight (DDNet
	// CClient purges via PurgeUntil(min(DeltaTick, prev, current))). This keeps
	// retention proportional to the live delta window — a handful of snaps —
	// instead of a fixed history, which dominates heap when many clients run.
	if deltaTick >= 0 {
		for t := range ss.Snaps {
			if t < baseTick {
				delete(ss.Snaps, t)
			}
		}
	}
	// Hard cap as a safety net against pathological ack lag.
	if len(ss.Snaps) > ss.MaxSnaps {
		for t := range ss.Snaps {
			if t < tick-ss.MaxSnaps {
				delete(ss.Snaps, t)
			}
		}
	}

	return snap, nil
}

type itemKey struct {
	typeID, id int
}

func applyDelta(base *Snapshot, tick int, data []byte, itemSizeFn func(int) int) (*Snapshot, error) {
	if len(data) == 0 {
		// Empty delta: carry the base forward verbatim. Fields slices are
		// read-only after creation, so share them instead of deep-copying
		// (mirrors DDNet's carry-forward of unchanged item data; avoids a
		// per-item allocation every tick — the dominant heap cost at scale).
		return &Snapshot{Tick: tick, Items: append([]SnapItem(nil), base.Items...)}, nil
	}

	sc := scratchPool.Get().(*deltaScratch)
	defer func() {
		clear(sc.base)
		clear(sc.deleted)
		clear(sc.idx)
		scratchPool.Put(sc)
	}()

	// Copy data into the scratch unpacker's reused buffer (no per-call alloc).
	u := &sc.u
	u.Reset(data)

	numDeleted, err := u.GetInt()
	if err != nil {
		return nil, fmt.Errorf("numDeleted: %w", err)
	}
	numUpdated, err := u.GetInt()
	if err != nil {
		return nil, fmt.Errorf("numUpdated: %w", err)
	}
	if _, err := u.GetInt(); err != nil {
		return nil, fmt.Errorf("unused: %w", err)
	}

	baseMap := sc.base
	for _, item := range base.Items {
		baseMap[itemKey{item.TypeID, item.ID}] = item.Fields
	}

	deleted := sc.deleted
	for i := range numDeleted {
		key, err := u.GetInt()
		if err != nil {
			return nil, fmt.Errorf("deleted key %d: %w", i, err)
		}
		typeID := (key >> 16) & 0xFFFF
		id := key & 0xFFFF
		deleted[itemKey{typeID, id}] = true
	}

	// Carry non-deleted base items forward (sharing their read-only Fields
	// slice — no per-item copy) and index them by key so the updated-item
	// merge below is O(1) instead of an O(numUpdated × items) linear scan (V50).
	result := &Snapshot{Tick: tick}
	idx := sc.idx
	for _, item := range base.Items {
		k := itemKey{item.TypeID, item.ID}
		if !deleted[k] {
			idx[k] = len(result.Items)
			result.Items = append(result.Items, item)
		}
	}

	// Pass 1: decode every updated item's absolute fields into one flat scratch
	// buffer, recording each item's offset/size. This lets all retained Fields
	// share a SINGLE backing allocation (V51) instead of one make([]int) per
	// item (the dominant heap cost at scale — see §PERF T35).
	flat := sc.flat[:0]
	upds := sc.upds[:0]
	for i := range numUpdated {
		typeID, err := u.GetInt()
		if err != nil {
			return nil, fmt.Errorf("updated type %d: %w", i, err)
		}
		id, err := u.GetInt()
		if err != nil {
			return nil, fmt.Errorf("updated id %d: %w", i, err)
		}

		size := -1
		if itemSizeFn != nil {
			size = itemSizeFn(typeID)
		}
		if size < 0 {
			size, err = u.GetInt()
			if err != nil {
				return nil, fmt.Errorf("updated size %d (type %d): %w", i, typeID, err)
			}
		}

		deltaFields := sc.deltaBuf(size)
		for f := 0; f < size; f++ {
			v, err := u.GetInt()
			if err != nil {
				break
			}
			deltaFields[f] = v
		}

		baseFields := baseMap[itemKey{typeID, id}]
		off := len(flat)
		for f := 0; f < size; f++ {
			b := 0
			if f < len(baseFields) {
				b = baseFields[f]
			}
			flat = append(flat, b+deltaFields[f])
		}
		upds = append(upds, updMeta{typeID: typeID, id: id, off: off, size: size})
	}
	sc.flat = flat // keep the grown buffer for reuse
	sc.upds = upds

	// One allocation for all updated items' fields; sub-slice per item with a
	// capped 3-index slice so an item's Fields can't append into a neighbour.
	backing := make([]int, len(flat))
	copy(backing, flat)

	// Pass 2: merge updated items into the result via the O(1) index (V50).
	for _, ud := range upds {
		fields := backing[ud.off : ud.off+ud.size : ud.off+ud.size]
		k := itemKey{ud.typeID, ud.id}
		if j, ok := idx[k]; ok {
			result.Items[j].Fields = fields
		} else {
			idx[k] = len(result.Items)
			result.Items = append(result.Items, SnapItem{TypeID: ud.typeID, ID: ud.id, Fields: fields})
		}
	}

	return result, nil
}

// SnapAssemblyState buffers multi-part snapshots until all parts arrive.
type SnapAssemblyState struct {
	Tick      int
	DeltaTick int
	NumParts  int
	Received  int
	Parts     [][]byte
}
