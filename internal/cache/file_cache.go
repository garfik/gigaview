package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileCache implements file-based cache
// Structure: {cacheDir}/{imageID}_{tileSize}_{maxZoom}/{z}/{x}_{y}.jpg
type FileCache struct {
	mu       sync.RWMutex
	cacheDir string
}

func NewFileCache(cacheDir string) (*FileCache, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &FileCache{
		cacheDir: cacheDir,
	}, nil
}

// buildFilePath builds file path from tile key
// Structure: {cacheDir}/{imageID}_{tileSize}_{maxZoom}/{z}/{x}_{y}.{format}
func (c *FileCache) buildFilePath(key TileKey) string {
	dirName := fmt.Sprintf("%s_%d_%d", key.ImageID, key.TileSize, key.MaxZoom)
	dir := filepath.Join(c.cacheDir, dirName, fmt.Sprintf("%d", key.Z))
	fileName := fmt.Sprintf("%d_%d.%s", key.X, key.Y, key.Format)
	return filepath.Join(dir, fileName)
}

func (c *FileCache) Get(key TileKey) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	filePath := c.buildFilePath(key)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, false
	}

	return data, true
}

func (c *FileCache) Set(key TileKey, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	filePath := c.buildFilePath(key)
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	// Write atomically
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, value, 0644); err != nil {
		return
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return
	}
}

func (c *FileCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.RemoveAll(c.cacheDir); err != nil {
		return
	}

	os.MkdirAll(c.cacheDir, 0755)
}
