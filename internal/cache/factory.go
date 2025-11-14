package cache

import (
	"fmt"

	"go.uber.org/zap"
)

// NewCache creates a cache instance based on the cache type
func NewCache(cacheType, cacheFileDir string, cacheMemoryTiles int, log *zap.Logger) (Cache, error) {
	switch cacheType {
	case "memory":
		log.Info("Using memory cache", zap.Int("max_tiles", cacheMemoryTiles))
		return NewMemoryCache(cacheMemoryTiles), nil
	case "file":
		log.Info("Using file cache", zap.String("cache_dir", cacheFileDir))
		return NewFileCache(cacheFileDir)
	case "disabled":
		log.Info("Cache disabled")
		return NewNoopCache(), nil
	default:
		return nil, fmt.Errorf("unknown cache type: %s (supported: memory, file, disabled)", cacheType)
	}
}
