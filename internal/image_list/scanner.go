package image_list

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cshum/vipsgen/vips"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ImageInfo struct {
	ID               string `json:"id"`
	OriginalFilename string `json:"original_filename"`
	CurrentFilename  string `json:"current_filename"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	Bytes            int64  `json:"bytes"`
}

type Scanner struct {
	dataDir string
	logger  *zap.Logger
	images  []ImageInfo
}

func New(dataDir string, logger *zap.Logger) *Scanner {
	return &Scanner{
		dataDir: dataDir,
		logger:  logger,
		images:  []ImageInfo{},
	}
}

func (s *Scanner) Scan() error {
	s.images = []ImageInfo{}

	extensions := map[string]bool{
		".tif":  true,
		".tiff": true,
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".webp": true,
	}

	if err := s.cleanupOrphanedJSON(); err != nil {
		return err
	}

	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return fmt.Errorf("failed to read data directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := s.getFilePath(entry.Name())
		info, err := entry.Info()
		if err != nil {
			s.logger.Warn("Error getting file info", zap.String("path", path), zap.Error(err))
			continue
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !extensions[ext] {
			continue
		}

		basename := strings.TrimSuffix(filepath.Base(path), ext)
		jsonPath := s.getFilePath(basename + ".json")

		var imageInfo *ImageInfo
		var finalPath string

		// If there is no metadata, we need to create it and rename the file
		if _, err := os.Stat(jsonPath); err != nil {
			newUUID := uuid.New().String()
			finalPath = s.getFilePath(newUUID + ext)
			if err := os.Rename(path, finalPath); err != nil {
				s.logger.Warn("Failed to rename file", zap.String("old_path", path), zap.String("new_path", finalPath), zap.Error(err))
				continue
			}
			s.logger.Info("Migrated file to UUID", zap.String("old_path", path), zap.String("new_path", finalPath))

			imageInfo, err = s.scanImage(finalPath, info)
			if err != nil {
				s.logger.Warn("Failed to scan image", zap.String("path", finalPath), zap.Error(err))
				continue
			}

			imageInfo.ID = newUUID
			imageInfo.OriginalFilename = filepath.Base(path)
			imageInfo.CurrentFilename = filepath.Base(finalPath)

			jsonPath = s.getFilePath(newUUID + ".json")
			if err := s.saveMetadata(jsonPath, imageInfo); err != nil {
				s.logger.Warn("Failed to save metadata", zap.String("json_path", jsonPath), zap.Error(err))
			} else {
				s.logger.Info("Created metadata file", zap.String("json_path", jsonPath))
			}
		} else {
			// Metadata exists, load it
			imageInfo, err = s.loadMetadata(jsonPath)
			if err != nil {
				s.logger.Warn("Failed to load metadata, skipping", zap.String("json_path", jsonPath), zap.Error(err))
				continue
			}
		}
		s.images = append(s.images, *imageInfo)
	}

	return nil
}

func (s *Scanner) cleanupOrphanedJSON() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return fmt.Errorf("failed to read data directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := s.getFilePath(entry.Name())
		if strings.ToLower(filepath.Ext(path)) != ".json" {
			continue
		}

		// Get ID from filename (basename without .json)
		basename := strings.TrimSuffix(filepath.Base(path), ".json")

		// Try to load metadata
		meta, err := s.loadMetadata(path)
		if err != nil {
			if err := os.Remove(path); err != nil {
				s.logger.Warn("Failed to delete invalid JSON", zap.String("path", path), zap.Error(err))
			} else {
				s.logger.Info("Deleted invalid JSON file", zap.String("path", path))
			}
			continue
		}

		// Validate that ID in JSON matches filename
		if meta.ID != basename {
			s.logger.Warn("UUID mismatch in JSON",
				zap.String("json_path", path),
				zap.String("filename_uuid", basename),
				zap.String("json_uuid", meta.ID))
			// Delete invalid JSON
			if err := os.Remove(path); err != nil {
				s.logger.Warn("Failed to delete invalid JSON", zap.String("path", path), zap.Error(err))
			} else {
				s.logger.Info("Deleted JSON with UUID mismatch", zap.String("path", path))
			}
			continue
		}

		imagePath := s.getFilePath(meta.CurrentFilename)
		if _, err := os.Stat(imagePath); err != nil {
			if err := os.Remove(path); err != nil {
				s.logger.Warn("Failed to delete orphaned JSON", zap.String("path", path), zap.Error(err))
			} else {
				s.logger.Info("Deleted orphaned JSON file", zap.String("path", path))
			}
		}
	}

	return nil
}

func (s *Scanner) scanImage(path string, info os.FileInfo) (*ImageInfo, error) {
	// Load image based on file extension
	image, err := s.loadImage(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %w", err)
	}
	defer image.Close()

	width := image.Width()
	height := image.Height()
	bytes := info.Size()

	id := uuid.New().String()

	return &ImageInfo{
		ID:     id,
		Width:  width,
		Height: height,
		Bytes:  bytes,
	}, nil
}

// loadImage loads an image based on file extension
func (s *Scanner) loadImage(path string) (*vips.Image, error) {
	ext := strings.ToLower(filepath.Ext(path))

	// Use AccessSequential for scanning (just need dimensions)
	access := vips.AccessSequential

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

func (s *Scanner) GetImages() []ImageInfo {
	return s.images
}

func (s *Scanner) GetImageByID(id string) *ImageInfo {
	for _, img := range s.images {
		if img.ID == id {
			return &img
		}
	}
	return nil
}

func (s *Scanner) GetImagePathByID(id string) string {
	imageInfo := s.GetImageByID(id)
	if imageInfo == nil {
		return ""
	}
	return s.getFilePath(imageInfo.CurrentFilename)
}

func (s *Scanner) getFilePath(filename string) string {
	return filepath.Join(s.dataDir, filename)
}

func (s *Scanner) loadMetadata(path string) (*ImageInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var meta ImageInfo
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &meta, nil
}

func (s *Scanner) saveMetadata(path string, meta *ImageInfo) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	return nil
}

// ProcessUploadedFile processes an uploaded file: generates UUID, saves as UUID.ext, creates metadata
func (s *Scanner) ProcessUploadedFile(tempPath string, originalFilename string) (string, error) {
	// Get file extension
	ext := strings.ToLower(filepath.Ext(originalFilename))

	// Generate UUID
	newUUID := uuid.New().String()

	// Determine final path
	finalPath := s.getFilePath(newUUID + ext)

	// Move/rename the temp file to final location
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", fmt.Errorf("failed to move uploaded file: %w", err)
	}

	// Get file info
	info, err := os.Stat(finalPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat file: %w", err)
	}

	// Scan image to get dimensions
	imageInfo, err := s.scanImage(finalPath, info)
	if err != nil {
		return "", fmt.Errorf("failed to scan image: %w", err)
	}

	imageInfo.ID = newUUID
	imageInfo.OriginalFilename = originalFilename
	imageInfo.CurrentFilename = filepath.Base(finalPath)

	// Save metadata
	jsonPath := s.getFilePath(newUUID + ".json")
	if err := s.saveMetadata(jsonPath, imageInfo); err != nil {
		return "", fmt.Errorf("failed to save metadata: %w", err)
	}

	s.logger.Info("Processed uploaded file",
		zap.String("uuid", newUUID),
		zap.String("original_filename", originalFilename),
		zap.String("final_path", finalPath))

	return newUUID, nil
}
