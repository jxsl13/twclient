package net6

import (
	"context"
	"sync"
	"testing"
	"time"
)

// V145: DownloadMap reports progress (received, total) as map bytes arrive.
func TestMapDownloadProgress(t *testing.T) {
	addr := fullMockServer(t)
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

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	if err := s.Login(ctx, "tester", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}
	_, _ = s.DownloadMap(ctx) // stub bytes may fail to parse; progress fires during recv

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("no map-download progress callbacks fired")
	}
	prev := 0
	for i, c := range calls {
		if c[1] != 16 { // total = MAP_CHANGE size (mock advertises 16)
			t.Errorf("call %d total=%d, want 16", i, c[1])
		}
		if c[0] < prev {
			t.Errorf("call %d received=%d went backwards from %d", i, c[0], prev)
		}
		prev = c[0]
	}
}
