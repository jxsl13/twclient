package packet

import (
	"fmt"

	"github.com/jxsl13/tw-protocol/packer"
)

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

// NewSnapStorage creates a new snap storage with the given item size function.
// Pass nil to always read sizes from the stream.
func NewSnapStorage(itemSizeFn func(typeID int) int) *SnapStorage {
	return &SnapStorage{
		Snaps:      make(map[int]*Snapshot),
		MaxSnaps:   256,
		ItemSizeFn: itemSizeFn,
	}
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
		result := &Snapshot{Tick: tick}
		for _, item := range base.Items {
			fields := make([]int, len(item.Fields))
			copy(fields, item.Fields)
			result.Items = append(result.Items, SnapItem{
				TypeID: item.TypeID,
				ID:     item.ID,
				Fields: fields,
			})
		}
		return result, nil
	}

	u := packer.NewUnpacker(data)

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

	baseMap := make(map[itemKey][]int, len(base.Items))
	for _, item := range base.Items {
		baseMap[itemKey{item.TypeID, item.ID}] = item.Fields
	}

	deleted := make(map[itemKey]bool, numDeleted)
	for i := range numDeleted {
		key, err := u.GetInt()
		if err != nil {
			return nil, fmt.Errorf("deleted key %d: %w", i, err)
		}
		typeID := (key >> 16) & 0xFFFF
		id := key & 0xFFFF
		deleted[itemKey{typeID, id}] = true
	}

	result := &Snapshot{Tick: tick}
	for _, item := range base.Items {
		k := itemKey{item.TypeID, item.ID}
		if !deleted[k] {
			fields := make([]int, len(item.Fields))
			copy(fields, item.Fields)
			result.Items = append(result.Items, SnapItem{
				TypeID: item.TypeID,
				ID:     item.ID,
				Fields: fields,
			})
		}
	}

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

		deltaFields := make([]int, size)
		for f := 0; f < size; f++ {
			v, err := u.GetInt()
			if err != nil {
				break
			}
			deltaFields[f] = v
		}

		k := itemKey{typeID, id}
		baseFields := baseMap[k]
		if baseFields == nil {
			baseFields = make([]int, size)
		}

		absFields := make([]int, size)
		for f := 0; f < size; f++ {
			b := 0
			if f < len(baseFields) {
				b = baseFields[f]
			}
			absFields[f] = b + deltaFields[f]
		}

		found := false
		for j := range result.Items {
			if result.Items[j].TypeID == typeID && result.Items[j].ID == id {
				result.Items[j].Fields = absFields
				found = true
				break
			}
		}
		if !found {
			result.Items = append(result.Items, SnapItem{
				TypeID: typeID,
				ID:     id,
				Fields: absFields,
			})
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
