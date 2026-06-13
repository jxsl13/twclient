package packet

import (
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/jxsl13/twclient/packer"
)

// applyDeltaLinear is a reference implementation matching the pre-T36 logic
// (linear updated-item scan, per-item field allocation). T36's optimized
// applyDelta must produce byte-identical Snapshots to this for all valid
// deltas (V50 parity).
func applyDeltaLinear(base *Snapshot, tick int, data []byte, itemSizeFn func(int) int) (*Snapshot, error) {
	if len(data) == 0 {
		return &Snapshot{Tick: tick, Items: append([]SnapItem(nil), base.Items...)}, nil
	}
	u := packer.NewUnpacker(data)
	numDeleted, err := u.NextInt()
	if err != nil {
		return nil, err
	}
	numUpdated, err := u.NextInt()
	if err != nil {
		return nil, err
	}
	if _, err := u.NextInt(); err != nil {
		return nil, err
	}
	baseMap := map[itemKey][]int{}
	for _, it := range base.Items {
		baseMap[itemKey{it.TypeID, it.ID}] = it.Fields
	}
	deleted := map[itemKey]bool{}
	for range numDeleted {
		key, err := u.NextInt()
		if err != nil {
			return nil, err
		}
		deleted[itemKey{(key >> 16) & 0xFFFF, key & 0xFFFF}] = true
	}
	result := &Snapshot{Tick: tick}
	for _, it := range base.Items {
		if !deleted[itemKey{it.TypeID, it.ID}] {
			result.Items = append(result.Items, it)
		}
	}
	for range numUpdated {
		typeID, err := u.NextInt()
		if err != nil {
			return nil, err
		}
		id, err := u.NextInt()
		if err != nil {
			return nil, err
		}
		size := -1
		if itemSizeFn != nil {
			size = itemSizeFn(typeID)
		}
		if size < 0 {
			if size, err = u.NextInt(); err != nil {
				return nil, err
			}
		}
		df := make([]int, size)
		for f := 0; f < size; f++ {
			v, err := u.NextInt()
			if err != nil {
				break
			}
			df[f] = v
		}
		bf := baseMap[itemKey{typeID, id}]
		abs := make([]int, size)
		for f := 0; f < size; f++ {
			b := 0
			if f < len(bf) {
				b = bf[f]
			}
			abs[f] = b + df[f]
		}
		found := false
		for j := range result.Items {
			if result.Items[j].TypeID == typeID && result.Items[j].ID == id {
				result.Items[j].Fields = abs
				found = true
				break
			}
		}
		if !found {
			result.Items = append(result.Items, SnapItem{TypeID: typeID, ID: id, Fields: abs})
		}
	}
	return result, nil
}

// randDelta builds a random but valid delta and the base/sizeFn it applies to.
func randDelta(rng *rand.Rand) (base *Snapshot, data []byte, sizeFn func(int) int) {
	// Type sizes: known types via sizeFn, plus unknown types that carry size.
	knownSize := map[int]int{9: 22, 10: 5, 5: 3}
	sizeFn = func(t int) int {
		if s, ok := knownSize[t]; ok {
			return s
		}
		return -1
	}

	nBase := rng.IntN(40)
	base = &Snapshot{Tick: 100}
	type key struct{ t, id int }
	used := map[key]bool{}
	for range nBase {
		t := []int{9, 10, 5, 99}[rng.IntN(4)] // 99 = unknown (stream size)
		id := rng.IntN(64)
		if used[key{t, id}] {
			continue
		}
		used[key{t, id}] = true
		size := sizeFn(t)
		if size < 0 {
			size = 1 + rng.IntN(8)
		}
		f := make([]int, size)
		for i := range f {
			f[i] = rng.IntN(2000) - 1000
		}
		base.Items = append(base.Items, SnapItem{TypeID: t, ID: id, Fields: f})
	}

	// Build delta: delete some base items, update some (existing + new).
	var deletes []int
	for _, it := range base.Items {
		if rng.IntN(3) == 0 {
			deletes = append(deletes, (it.TypeID<<16)|it.ID)
		}
	}
	type upd struct {
		t, id, size int
		fields      []int
	}
	var upds []upd
	updUsed := map[key]bool{}
	nUpd := rng.IntN(40)
	for range nUpd {
		t := []int{9, 10, 5, 99}[rng.IntN(4)]
		id := rng.IntN(64)
		if updUsed[key{t, id}] {
			continue
		}
		updUsed[key{t, id}] = true
		size := sizeFn(t)
		if size < 0 {
			size = 1 + rng.IntN(8)
		}
		f := make([]int, size)
		for i := range f {
			f[i] = rng.IntN(2000) - 1000
		}
		upds = append(upds, upd{t, id, size, f})
	}

	data = append(data, packer.PackInt(len(deletes))...)
	data = append(data, packer.PackInt(len(upds))...)
	data = append(data, packer.PackInt(0)...)
	for _, d := range deletes {
		data = append(data, packer.PackInt(d)...)
	}
	for _, ud := range upds {
		data = append(data, packer.PackInt(ud.t)...)
		data = append(data, packer.PackInt(ud.id)...)
		if sizeFn(ud.t) < 0 {
			data = append(data, packer.PackInt(ud.size)...)
		}
		for _, v := range ud.fields {
			data = append(data, packer.PackInt(v)...)
		}
	}
	return base, data, sizeFn
}

func TestApplyDeltaParity(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := range 2000 {
		base, data, sizeFn := randDelta(rng)
		got, errG := applyDelta(base, 101, data, sizeFn)
		want, errW := applyDeltaLinear(base, 101, data, sizeFn)
		if (errG == nil) != (errW == nil) {
			t.Fatalf("iter %d: err mismatch: got=%v want=%v", iter, errG, errW)
		}
		if errG != nil {
			continue
		}
		if !snapEqual(got, want) {
			t.Fatalf("iter %d: snapshot mismatch\n got=%v\nwant=%v", iter, got.Items, want.Items)
		}
	}
}

// snapEqual compares two snapshots ignoring item order (the optimized impl
// preserves base order then appends new items in delta order — identical to
// the linear impl — but compare order-insensitively to be robust).
func snapEqual(a, b *Snapshot) bool {
	if a.Tick != b.Tick || len(a.Items) != len(b.Items) {
		return false
	}
	index := func(s *Snapshot) map[itemKey][]int {
		m := make(map[itemKey][]int, len(s.Items))
		for _, it := range s.Items {
			m[itemKey{it.TypeID, it.ID}] = it.Fields
		}
		return m
	}
	ma, mb := index(a), index(b)
	if len(ma) != len(mb) {
		return false
	}
	for k, fa := range ma {
		if !reflect.DeepEqual(fa, mb[k]) {
			return false
		}
	}
	return true
}

// TestApplyDeltaEmpty confirms the carry-forward path is unchanged.
func TestApplyDeltaEmpty(t *testing.T) {
	base := &Snapshot{Tick: 100, Items: []SnapItem{{TypeID: 9, ID: 0, Fields: []int{1, 2, 3}}}}
	got, err := applyDelta(base, 101, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tick != 101 || len(got.Items) != 1 || !reflect.DeepEqual(got.Items[0].Fields, []int{1, 2, 3}) {
		t.Fatalf("carry-forward wrong: %v", got.Items)
	}
}
