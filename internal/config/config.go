package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Port             int
	DataDir          string
	WarmupLevels     int
	WarmupWorkers    int
	CacheType        string
	CacheMemoryTiles int
	CacheFileDir     string
	VipsMaxCacheMB   int
	VipsConcurrency  int
	LogLevel         string
	UploadToken      string
	MaxUploadSize    int64
	AllowedOrigin    string
	PublicBaseURL    string
}

func Load() *Config {
	dataDir := getEnv("DATA_DIR", "/data")
	cacheType := getEnv("CACHE", "memory")

	cfg := &Config{
		Port:             getEnvInt("PORT", 8080),
		DataDir:          dataDir,
		WarmupLevels:     getEnvInt("WARMUP_LEVELS", 1),
		WarmupWorkers:    getEnvInt("WARMUP_WORKERS", 1),
		CacheType:        cacheType,
		CacheMemoryTiles: getEnvInt("CACHE_MEMORY_TILES", 2000),
		CacheFileDir:     getEnv("CACHE_FILE_DIR", filepath.Join(dataDir, "cache")),
		VipsMaxCacheMB:   getEnvInt("VIPS_MAX_CACHE_MB", 256),
		VipsConcurrency:  getEnvInt("VIPS_CONCURRENCY", 1),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		UploadToken:      getEnv("UPLOAD_TOKEN", ""),
		MaxUploadSize:    getEnvInt64("MAX_UPLOAD_SIZE", 4294967296), // 4GB default
		AllowedOrigin:    getEnv("ALLOWED_ORIGIN", ""),
		PublicBaseURL:    getEnv("PUBLIC_BASE_URL", "http://localhost:8080"),
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func (c *Config) IsUploadPublic() bool {
	return strings.TrimSpace(c.UploadToken) == ""
}
