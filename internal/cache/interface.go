package cache

// TileKey represents the parameters for a tile cache key
type TileKey struct {
	ImageID  string
	TileSize int
	MaxZoom  int
	Z        int
	X        int
	Y        int
	Format   string
}

type Cache interface {
	Get(key TileKey) ([]byte, bool)
	Set(key TileKey, value []byte)
	Clear()
}
