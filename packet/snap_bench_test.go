package packet

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
)

// charSizeFn mirrors net6.SnapItemSize for the character object (type 9, 22
// fields), letting applyDelta skip the per-item size varint. packet cannot
// import net6 (net6 imports packet), so this local stand-in is used.
func charSizeFn(typeID int) int {
	if typeID == benchCharType {
		return benchCharSize
	}
	return -1
}

const (
	benchCharType = 9
	benchCharSize = 22
	benchChars    = 64
)

// buildBaseSnap makes a base snapshot with n character items (type 9, 22 fields).
func buildBaseSnap(n int) *Snapshot {
	s := &Snapshot{Tick: 100}
	for id := range n {
		f := make([]int, benchCharSize)
		for k := range f {
			f[k] = id*100 + k
		}
		s.Items = append(s.Items, SnapItem{TypeID: benchCharType, ID: id, Fields: f})
	}
	return s
}

// buildDelta makes a delta that updates all n character items (+1 per field).
func buildDelta(n int) []byte {
	var b []byte
	b = append(b, packer.PackInt(0)...) // numDeleted
	b = append(b, packer.PackInt(n)...) // numUpdated
	b = append(b, packer.PackInt(0)...) // unused
	for id := range n {
		b = append(b, packer.PackInt(benchCharType)...)
		b = append(b, packer.PackInt(id)...)
		for range benchCharSize {
			b = append(b, packer.PackInt(1)...)
		}
	}
	return b
}

// BenchmarkApplyDelta exercises the full-update hot path: every character item
// is updated each tick. With 64 chars this stresses the updated-item lookup
// (the O(n^2) scan T36 replaces).
func BenchmarkApplyDelta(b *testing.B) {
	base := buildBaseSnap(benchChars)
	delta := buildDelta(benchChars)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := applyDelta(base, 101, delta, charSizeFn); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkApplyDeltaEmpty measures the carry-forward (empty delta) path.
func BenchmarkApplyDeltaEmpty(b *testing.B) {
	base := buildBaseSnap(benchChars)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := applyDelta(base, 101, nil, charSizeFn); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProcessSnap measures the full storage path (delta + retention).
func BenchmarkProcessSnap(b *testing.B) {
	delta := buildDelta(benchChars)
	base := buildBaseSnap(benchChars)
	b.ReportAllocs()
	for b.Loop() {
		ss := NewSnapStorage(charSizeFn)
		ss.Snaps[100] = base
		if _, err := ss.ProcessSnap(101, 1, delta); err != nil {
			b.Fatal(err)
		}
	}
}
