package net7

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// V145: DownloadMap reports progress (received, total) as map bytes arrive over
// the 0.7 request path.
func TestMapDownloadProgress07(t *testing.T) {
	addr := mapDataResponder(t, 32)
	var mu sync.Mutex
	var calls [][2]int
	s, err := NewSession(addr, WithMapDownloadProgress(func(received, total int) {
		mu.Lock()
		calls = append(calls, [2]int{received, total})
		mu.Unlock()
	}))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.mapMu.Lock()
	s.mapInfo = packet.MapInfo{Name: "m", Size: 16}
	s.mapMu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _ = s.DownloadMap(ctx) // stub bytes may fail to parse; progress fires during recv

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("no map-download progress callbacks fired")
	}
	prev := 0
	for i, c := range calls {
		if c[1] != 16 { // total = info.Size
			t.Errorf("call %d total=%d, want 16", i, c[1])
		}
		if c[0] < prev {
			t.Errorf("call %d received=%d went backwards from %d", i, c[0], prev)
		}
		prev = c[0]
	}
}
