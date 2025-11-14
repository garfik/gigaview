package cache

type NoopCache struct{}

func NewNoopCache() *NoopCache {
	return &NoopCache{}
}

func (c *NoopCache) Get(key TileKey) ([]byte, bool) {
	return nil, false
}

func (c *NoopCache) Set(key TileKey, value []byte) {
}

func (c *NoopCache) Clear() {
}
