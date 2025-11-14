package cache

import (
	"container/list"
	"sync"
)

type entry struct {
	key   TileKey
	value []byte
}

// MemoryCache implements in-memory LRU cache
type MemoryCache struct {
	mu      sync.RWMutex
	maxSize int
	items   map[TileKey]*list.Element
	lruList *list.List
}

// NewMemoryCache creates a new in-memory LRU cache
func NewMemoryCache(maxSize int) *MemoryCache {
	return &MemoryCache{
		maxSize: maxSize,
		items:   make(map[TileKey]*list.Element),
		lruList: list.New(),
	}
}

func (c *MemoryCache) Has(key TileKey) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.items[key]
	return ok
}

func (c *MemoryCache) Get(key TileKey) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	c.lruList.MoveToFront(elem)
	return elem.Value.(*entry).value, true
}

func (c *MemoryCache) Set(key TileKey, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		elem.Value.(*entry).value = value
		c.lruList.MoveToFront(elem)
		return
	}

	if c.lruList.Len() >= c.maxSize {
		oldest := c.lruList.Back()
		if oldest != nil {
			delete(c.items, oldest.Value.(*entry).key)
			c.lruList.Remove(oldest)
		}
	}

	ent := &entry{key: key, value: value}
	elem := c.lruList.PushFront(ent)
	c.items[key] = elem
}

func (c *MemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[TileKey]*list.Element)
	c.lruList = list.New()
}
