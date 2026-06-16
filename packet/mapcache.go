package packet

import (
	"bytes"
	"encoding/hex"
	"sync"

	"github.com/jxsl13/twmap"
)

// MapCache is a thread-safe cache for parsed Teeworlds maps.
// Multiple sessions can share one cache so that only the first bot
// on a given map actually downloads and parses it; all others wait
// without holding any locks.
type MapCache struct {
	mu      sync.Mutex
	maps    map[string]*twmap.Map
	pending map[string]chan struct{}
}

// NewMapCache creates an empty map cache.
func NewMapCache() *MapCache {
	return &MapCache{
		maps:    make(map[string]*twmap.Map),
		pending: make(map[string]chan struct{}),
	}
}

func mapCacheKey(name string, sha256 [32]byte) string {
	return name + ":" + hex.EncodeToString(sha256[:])
}

// Get returns a cached map, or nil if not cached.
func (c *MapCache) Get(name string, sha256 [32]byte) *twmap.Map {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maps[mapCacheKey(name, sha256)]
}

// GetOrWait checks the cache for a map. Three outcomes:
//   - Already cached: returns (map, true) immediately.
//   - Another goroutine is downloading: releases the lock, waits on a
//     channel (no mutex held), then returns (map, true/false).
//   - Nobody is downloading: registers this goroutine as the downloader,
//     returns (nil, false). Caller must call Put or PutFailed when done.
func (c *MapCache) GetOrWait(name string, sha256 [32]byte) (*twmap.Map, bool) {
	key := mapCacheKey(name, sha256)
	c.mu.Lock()

	if m, ok := c.maps[key]; ok {
		c.mu.Unlock()
		return m, true
	}

	if ch, ok := c.pending[key]; ok {
		c.mu.Unlock()
		<-ch
		c.mu.Lock()
		m := c.maps[key]
		c.mu.Unlock()
		return m, m != nil
	}

	ch := make(chan struct{})
	c.pending[key] = ch
	c.mu.Unlock()
	return nil, false
}

// Put stores a parsed map and wakes all goroutines waiting for it.
func (c *MapCache) Put(name string, sha256 [32]byte, m *twmap.Map) {
	key := mapCacheKey(name, sha256)
	c.mu.Lock()
	c.maps[key] = m
	ch := c.pending[key]
	delete(c.pending, key)
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// PutFailed removes the pending marker so waiting goroutines can retry.
func (c *MapCache) PutFailed(name string, sha256 [32]byte) {
	key := mapCacheKey(name, sha256)
	c.mu.Lock()
	ch := c.pending[key]
	delete(c.pending, key)
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// ParseAndPut parses raw map data, stores the result, and returns it.
func (c *MapCache) ParseAndPut(name string, sha256 [32]byte, data []byte) (*twmap.Map, error) {
	// Lenient: a headless client uses tiles/collision/game-state, not the
	// optional INFO metadata item, so accept maps that lack it (V146/B28).
	m, err := twmap.Parse(bytes.NewReader(data), twmap.WithRequireInfo(false))
	if err != nil {
		c.PutFailed(name, sha256)
		return nil, err
	}
	c.Put(name, sha256, m)
	return m, nil
}
