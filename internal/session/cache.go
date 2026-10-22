package session

import (
	"container/list"
	"fmt"
	"strings"
	"sync"
	"time"
)

type CacheLimits struct {
	Bytes      int
	Entries    int
	ValueBytes int
}

type Record struct {
	Data       []byte
	Error      string
	WrittenAt  time.Time
	FreshUntil time.Time
	ExpiresAt  time.Time
	Failures   int
}

type CacheStats struct {
	Bytes     int
	Entries   int
	Evictions uint64
}

type cacheEntry struct {
	key    string
	record Record
	size   int
}

type Cache struct {
	mu        sync.Mutex
	limits    CacheLimits
	order     *list.List
	entries   map[string]*list.Element
	bytes     int
	evictions uint64
}

func NewCache(limits CacheLimits) (*Cache, error) {
	if limits.Bytes <= 0 || limits.Entries <= 0 || limits.ValueBytes <= 0 || limits.ValueBytes > limits.Bytes {
		return nil, fmt.Errorf("invalid memory cache limits")
	}
	return &Cache{limits: limits, order: list.New(), entries: make(map[string]*list.Element)}, nil
}

func (c *Cache) Put(key string, record Record) error {
	size := len(key) + len(record.Data) + len(record.Error) + 256
	if key == "" || len(key) > 4096 || len(record.Data) > c.limits.ValueBytes || size > c.limits.Bytes {
		return fmt.Errorf("memory cache entry exceeds limits")
	}
	owned := record
	owned.Data = append([]byte(nil), record.Data...)
	owned.Error = strings.Clone(record.Error)
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.entries[key]; old != nil {
		c.remove(old)
	}
	for c.bytes+size > c.limits.Bytes || len(c.entries) >= c.limits.Entries {
		c.remove(c.order.Back())
		c.evictions++
	}
	entry := &cacheEntry{key: strings.Clone(key), record: owned, size: size}
	c.entries[entry.key] = c.order.PushFront(entry)
	c.bytes += size
	return nil
}

func (c *Cache) Get(key string, now time.Time) (Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[key]
	if element == nil {
		return Record{}, false
	}
	entry := element.Value.(*cacheEntry)
	if !now.Before(entry.record.ExpiresAt) {
		c.remove(element)
		return Record{}, false
	}
	c.order.MoveToFront(element)
	result := entry.record
	result.Data = append([]byte(nil), entry.record.Data...)
	return result, true
}

func (c *Cache) Invalidate(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, element := range c.entries {
		if strings.HasPrefix(key, prefix) {
			c.remove(element)
		}
	}
}

func (c *Cache) Sweep(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, element := range c.entries {
		if !now.Before(element.Value.(*cacheEntry).record.ExpiresAt) {
			c.remove(element)
		}
	}
}

func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CacheStats{Bytes: c.bytes, Entries: len(c.entries), Evictions: c.evictions}
}

func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.order.Len() > 0 {
		c.remove(c.order.Back())
	}
}

func (c *Cache) remove(element *list.Element) {
	entry := element.Value.(*cacheEntry)
	delete(c.entries, entry.key)
	c.bytes -= entry.size
	entry.key, entry.record.Data, entry.record.Error = "", nil, ""
	c.order.Remove(element)
	element.Value = nil
}
