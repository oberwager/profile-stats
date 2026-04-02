package cache

import (
	"sync"
	"time"
)

type entry struct {
	data    []byte
	expires time.Time
}

type Cache struct {
	mu      sync.Mutex
	entries map[string]entry
}

func New() *Cache {
	return &Cache{entries: make(map[string]entry)}
}

// Get returns cached bytes if not expired.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.data, true
}

// Set stores bytes with a TTL.
func (c *Cache) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{
		data:    data,
		expires: time.Now().Add(ttl),
	}
}
