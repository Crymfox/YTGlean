package feed

import (
	"sync"
	"time"
)

type cachedFeed struct {
	entries   []Entry
	fetchedAt time.Time
}

// Cache provides in-memory caching for RSS feed results.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*cachedFeed
	ttl     time.Duration
}

// NewCache creates a feed cache with the given TTL.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]*cachedFeed),
		ttl:     ttl,
	}
}

// Get returns cached entries for a channel if available and not expired.
func (c *Cache) Get(channelID string) ([]Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cached, ok := c.entries[channelID]
	if !ok || time.Since(cached.fetchedAt) > c.ttl {
		return nil, false
	}
	return cached.entries, true
}

// Set stores entries for a channel.
func (c *Cache) Set(channelID string, entries []Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[channelID] = &cachedFeed{
		entries:   entries,
		fetchedAt: time.Now(),
	}
}

// Clear removes all cached entries.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cachedFeed)
}
