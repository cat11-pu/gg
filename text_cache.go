package gg

import (
	"container/list"
	"image"
	"sync"
)

// Default glyph cache limits used by the enhanced text pipeline.
const (
	defaultCacheEntries = 512
	defaultCacheBytes   = 8 * 1024 * 1024
)

// GlyphCacheStats holds counters for a GlyphCache.
type GlyphCacheStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
	Entries   int
	Bytes     int
	MaxBytes  int
}

// HitRate returns hits/(hits+misses), or 0 when no lookup happened yet.
func (s GlyphCacheStats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// glyphCacheKey identifies a rasterized glyph. Cache identity of the face is
// tracked separately by the owning TextRenderer.
type glyphCacheKey struct {
	rune   rune
	quantX int
	quantY int
	scale  int // supersampling factor (1 or 2)
}

type glyphCacheEntry struct {
	key     glyphCacheKey
	mask    *image.Alpha
	bearing image.Point
	advance int // 26.6 fixed point, in target pixels
	bytes   int
}

// GlyphCache is an LRU cache of rasterized glyph alpha masks. The same glyph
// drawn repeatedly is rasterized only once. Both the number of entries and
// the total pixel memory are capped, so a long running process cannot grow
// without bound: when a limit is reached, the least recently used entry is
// evicted.
type GlyphCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	items      map[glyphCacheKey]*list.Element
	lru        *list.List
	hits       int64
	misses     int64
	evictions  int64
	bytes      int
}

// NewGlyphCache creates a GlyphCache. maxEntries caps the number of cached
// glyphs and maxBytes caps the total memory used by alpha masks. A non
// positive value disables that particular limit.
func NewGlyphCache(maxEntries, maxBytes int) *GlyphCache {
	if maxEntries <= 0 {
		maxEntries = 512
	}
	return &GlyphCache{
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
		items:      make(map[glyphCacheKey]*list.Element),
		lru:        list.New(),
	}
}

// lookup returns the cached entry for key and marks it most recently used.
func (c *GlyphCache) lookup(key glyphCacheKey) (*glyphCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.lru.MoveToFront(el)
		c.hits++
		return el.Value.(*glyphCacheEntry), true
	}
	c.misses++
	return nil, false
}

// insert stores an entry, evicting least recently used entries as needed to
// stay within both limits.
func (c *GlyphCache) insert(e *glyphCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[e.key]; ok {
		c.lru.MoveToFront(el)
		old := el.Value.(*glyphCacheEntry)
		c.bytes += e.bytes - old.bytes
		el.Value = e
	} else {
		el := c.lru.PushFront(e)
		c.items[e.key] = el
		c.bytes += e.bytes
	}
	for (c.maxEntries > 0 && c.lru.Len() > c.maxEntries) ||
		(c.maxBytes > 0 && c.bytes > c.maxBytes) {
		c.evictOldest()
	}
}

func (c *GlyphCache) evictOldest() {
	el := c.lru.Back()
	if el == nil {
		return
	}
	old := c.lru.Remove(el).(*glyphCacheEntry)
	delete(c.items, old.key)
	c.bytes -= old.bytes
	c.evictions++
}

// Stats returns a snapshot of the cache counters and current footprint.
func (c *GlyphCache) Stats() GlyphCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return GlyphCacheStats{
		Hits:      c.hits,
		Misses:    c.misses,
		Evictions: c.evictions,
		Entries:   c.lru.Len(),
		Bytes:     c.bytes,
		MaxBytes:  c.maxBytes,
	}
}

// Reset clears all cached glyphs and counters.
func (c *GlyphCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[glyphCacheKey]*list.Element)
	c.lru.Init()
	c.hits = 0
	c.misses = 0
	c.evictions = 0
	c.bytes = 0
}

// alphaSize reports the byte cost of an alpha mask with the given size.
func alphaSize(w, h int) int {
	return w * h
}
