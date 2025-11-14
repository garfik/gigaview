package image_renderer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/cshum/vipsgen/vips"
	"go.uber.org/zap"

	"gigaview/internal/cache"
	"gigaview/internal/image_list"
)

type Renderer struct {
	dataDir   string
	scanner   *image_list.Scanner
	tileCache cache.Cache
	logger    *zap.Logger
}

type TileResult struct {
	Data []byte
	ETag string
	Size int
}

func New(dataDir string, scanner *image_list.Scanner, tileCache cache.Cache, logger *zap.Logger) *Renderer {
	return &Renderer{
		dataDir:   dataDir,
		scanner:   scanner,
		tileCache: tileCache,
		logger:    logger,
	}
}

func (r *Renderer) CalculateMaxZoom(width, height int) int {
	maxDim := math.Max(float64(width), float64(height))
	scale := maxDim / 256.0
	maxZoom := int(math.Ceil(math.Log2(scale)))
	if maxZoom < 0 {
		return 0
	}
	return maxZoom
}

func (r *Renderer) RenderTile(imageID string, z, x, y int) (*TileResult, error) {
	imageInfo := r.scanner.GetImageByID(imageID)
	if imageInfo == nil {
		return nil, fmt.Errorf("image not found: %s", imageID)
	}

	format := "jpeg"

	maxZoom := r.CalculateMaxZoom(imageInfo.Width, imageInfo.Height)
	tileSize := 256.0

	cacheKey := cache.TileKey{
		ImageID:  imageID,
		TileSize: int(tileSize),
		MaxZoom:  maxZoom,
		Z:        z,
		X:        x,
		Y:        y,
		Format:   format,
	}

	if cached, ok := r.tileCache.Get(cacheKey); ok {
		etag := r.generateETag(cacheKey)
		return &TileResult{
			Data: cached,
			ETag: etag,
			Size: len(cached),
		}, nil
	}

	imagePath := r.scanner.GetImagePathByID(imageID)
	if imagePath == "" {
		return nil, fmt.Errorf("image path not found for id: %s", imageID)
	}

	// Load image based on file extension
	image, err := r.loadImage(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %w", err)
	}
	defer image.Close()

	if z > maxZoom {
		return nil, fmt.Errorf("zoom level %d exceeds max zoom %d", z, maxZoom)
	}

	// Calculate how many source pixels map to one tile at this zoom level.
	// At zoom 0, one tile = full image. Each zoom level halves the pixels per tile.
	pixelsPerTile := tileSize * math.Pow(2, float64(maxZoom-z))

	// Calculate tile boundaries in source image pixel coordinates.
	// Clamp to image dimensions to handle edge tiles that extend beyond the image.
	startX := int(float64(x) * pixelsPerTile)
	startY := int(float64(y) * pixelsPerTile)
	endX := int(math.Min(float64(startX)+pixelsPerTile, float64(imageInfo.Width)))
	endY := int(math.Min(float64(startY)+pixelsPerTile, float64(imageInfo.Height)))

	width := endX - startX
	height := endY - startY
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid tile bounds")
	}

	// Step 1: Extract the tile region from the source image. This is memory efficient because it doesn't load the entire image into memory.
	if err := image.ExtractArea(startX, startY, width, height); err != nil {
		return nil, fmt.Errorf("failed to extract area: %w", err)
	}

	// Step 2: Scale down to tile size using level-specific scale factor.
	// This ensures all tiles at the same zoom level have consistent scale.
	resizeScale := tileSize / pixelsPerTile

	resizeOpts := vips.DefaultResizeOptions()
	resizeOpts.Kernel = vips.KernelLanczos3
	if err := image.Resize(resizeScale, resizeOpts); err != nil {
		return nil, fmt.Errorf("failed to resize: %w", err)
	}

	// Step 3: Pad to exactly 256×256 if needed (edge tiles may be smaller)
	// Anchor at top-left (0,0) to maintain tile alignment.
	w := image.Width()
	h := image.Height()
	if w < 256 || h < 256 {
		embedOpts := vips.DefaultEmbedOptions()
		embedOpts.Extend = vips.ExtendBackground
		// Use background color for padding, as there is no alpha channel in JPEG
		embedOpts.Background = []float64{221, 221, 221} // #ddd
		if err := image.Embed(0, 0, 256, 256, embedOpts); err != nil {
			return nil, fmt.Errorf("failed to pad: %w", err)
		}
	}

	// Step 4: Export as JPEG, save to cache and return the result
	jpegOpts := vips.DefaultJpegsaveBufferOptions()
	jpegOpts.Q = 82
	jpegOpts.Interlace = false

	tileData, err := image.JpegsaveBuffer(jpegOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to export: %w", err)
	}

	r.tileCache.Set(cacheKey, tileData)

	etag := r.generateETag(cacheKey)
	return &TileResult{
		Data: tileData,
		ETag: etag,
		Size: len(tileData),
	}, nil
}

func (r *Renderer) generateETag(key cache.TileKey) string {
	keyStr := fmt.Sprintf("%s_%d_%d/%d/%d/%d.%s", key.ImageID, key.TileSize, key.MaxZoom, key.Z, key.X, key.Y, key.Format)
	hash := sha256.Sum256([]byte(keyStr))
	return hex.EncodeToString(hash[:])[:16]
}

func (r *Renderer) GetImageMeta(imageID string) (map[string]interface{}, error) {
	imageInfo := r.scanner.GetImageByID(imageID)
	if imageInfo == nil {
		return nil, fmt.Errorf("image not found: %s", imageID)
	}

	maxZoom := r.CalculateMaxZoom(imageInfo.Width, imageInfo.Height)

	return map[string]interface{}{
		"width":          imageInfo.Width,
		"height":         imageInfo.Height,
		"tileSize":       256,
		"maxZoom":        maxZoom,
		"bytes":          imageInfo.Bytes,
		"format":         "jpeg",
		"copyright_text": imageInfo.CopyrightText,
		"copyright_link": imageInfo.CopyrightLink,
	}, nil
}

// loadImage loads an image based on file extension
func (r *Renderer) loadImage(path string) (*vips.Image, error) {
	ext := strings.ToLower(filepath.Ext(path))

	// Use AccessRandom for efficient tile extraction from large files
	access := vips.AccessRandom

	switch ext {
	case ".tif", ".tiff":
		opts := vips.DefaultTiffloadOptions()
		opts.Access = access
		return vips.NewTiffload(path, opts)
	case ".jpg", ".jpeg":
		opts := vips.DefaultJpegloadOptions()
		opts.Access = access
		return vips.NewJpegload(path, opts)
	case ".png":
		opts := vips.DefaultPngloadOptions()
		opts.Access = access
		return vips.NewPngload(path, opts)
	case ".webp":
		opts := vips.DefaultWebploadOptions()
		opts.Access = access
		return vips.NewWebpload(path, opts)
	default:
		return nil, fmt.Errorf("unsupported image format: %s", ext)
	}
}
