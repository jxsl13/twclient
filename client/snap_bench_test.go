package client

import "testing"

func benchCharMap(n int) map[int]CharacterState {
	m := make(map[int]CharacterState, n)
	for i := range n {
		m[i] = CharacterState{Tick: 100, X: i * 32, Y: i * 16, Weapon: i % 6, Direction: 1}
	}
	return m
}

// BenchmarkDeriveEvents measures the per-tick snap-derived event diff over a
// full 64-player snapshot (T39 target: intermediate event slices + map churn).
func BenchmarkDeriveEvents(b *testing.B) {
	prev := benchCharMap(64)
	cur := benchCharMap(64)
	for i := range cur {
		c := cur[i]
		c.X += 100
		c.AttackTick++
		cur[i] = c
	}
	ss := &SnapStorage{characters: cur, prevCharacters: prev}
	b.ReportAllocs()
	for b.Loop() {
		_ = ss.deriveEvents()
	}
}

// BenchmarkCharactersCopy measures the per-tick character-map copy taken on
// every observation build.
func BenchmarkCharactersCopy(b *testing.B) {
	ss := &SnapStorage{characters: benchCharMap(64)}
	b.ReportAllocs()
	for b.Loop() {
		_ = ss.charactersCopy()
	}
}
