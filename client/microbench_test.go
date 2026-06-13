package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
)

// benchSnapshot builds a realistic n-character 0.6 snapshot.
func benchSnapshot(n int) *packet.Snapshot {
	items := make([]packet.SnapItem, 0, n)
	for i := range n {
		items = append(items, charItem(i, 0, i%6))
	}
	return &packet.Snapshot{Tick: 100, Items: items}
}

// benchClient builds a Client with a 64-char snapshot applied and a small map
// view — the steady state of a connected client (prediction off, the default).
func benchClient() *Client {
	c := &Client{sess: &stubSession{}, predTun: tuningFromRaw(nil)}
	c.snap.localCID = 0
	game := twmap.Layer{Kind: twmap.LayerKindGame, Width: 8, Height: 8, Tiles: make([]twmap.Tile, 64)}
	c.mapView = NewMapView(&twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game}}}})
	c.snap.processSnapshot(benchSnapshot(64))
	return c
}

// BenchmarkBuildTickState measures the per-tick observation build (snapshot
// entities + predicted characters + tune zone) — run ~50×/s per consumer.
func BenchmarkBuildTickState(b *testing.B) {
	c := benchClient()
	b.ReportAllocs()
	for b.Loop() {
		_ = c.buildTickState()
	}
}

// BenchmarkProcessSnapshot measures the per-tick snap-state update (extract
// characters, game info, race) that drives every accessor.
func BenchmarkProcessSnapshot(b *testing.B) {
	var ss SnapStorage
	ss.localCID = 0
	snap := benchSnapshot(64)
	b.ReportAllocs()
	for b.Loop() {
		ss.processSnapshot(snap)
	}
}

// BenchmarkPredictedCharacters measures the predicted-character map build
// (a per-tick copy taken by buildTickState and every PredictedCharacters call).
func BenchmarkPredictedCharacters(b *testing.B) {
	c := benchClient()
	b.ReportAllocs()
	for b.Loop() {
		_ = c.PredictedCharacters()
	}
}

// BenchmarkMapViewWindow measures the ego-centric tile-window crop used by ML
// observation (V26/V27) — a hot path during training.
func BenchmarkMapViewWindow(b *testing.B) {
	game := twmap.Layer{Kind: twmap.LayerKindGame, Width: 64, Height: 64, Tiles: make([]twmap.Tile, 64*64)}
	mv := NewMapView(&twmap.Map{Groups: []twmap.Group{{Layers: []twmap.Layer{game}}}})
	b.ReportAllocs()
	for b.Loop() {
		_ = mv.Window(32, 32, 16, 16) // 33×33 crop
	}
}

// BenchmarkPredictedTime measures the tick-clock update + input gate run every
// snapshot / input send.
func BenchmarkPredictedTime(b *testing.B) {
	var pt PredictedTime
	tick := 0
	b.ReportAllocs()
	for b.Loop() {
		tick++
		pt.OnSnapshot(tick)
		_, _, _ = pt.NextInput()
	}
}
