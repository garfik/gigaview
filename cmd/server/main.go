package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cshum/vipsgen/vips"
	"go.uber.org/zap"

	"gigaview/internal/cache"
	"gigaview/internal/config"
	httphandlers "gigaview/internal/http"
	"gigaview/internal/image_list"
	"gigaview/internal/image_renderer"
	"gigaview/internal/logger"
)

func main() {
	cfg := config.Load()

	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize logger: %v", err))
	}
	defer log.Sync()

	vipsConfig := &vips.Config{
		ConcurrencyLevel: cfg.VipsConcurrency,
		MaxCacheMem:      cfg.VipsMaxCacheMB * 1024 * 1024, // Convert MB to bytes
		MaxCacheFiles:    0,                                // Disable disk cache
		MaxCacheSize:     0,                                // Disable disk cache
		ReportLeaks:      false,
		CacheTrace:       false,
		VectorEnabled:    true,
	}

	// Set up logging
	vips.SetLogging(func(domain string, level vips.LogLevel, message string) {
		// Map vips log levels to zap levels
		if level >= vips.LogLevelError {
			log.Error("vips", zap.String("domain", domain), zap.Int("level", int(level)), zap.String("message", message))
		} else if level >= vips.LogLevelWarning {
			log.Warn("vips", zap.String("domain", domain), zap.Int("level", int(level)), zap.String("message", message))
		}
		// Ignore info/debug messages to keep logs clean
	}, vips.LogLevelError)

	vips.Startup(vipsConfig)
	defer vips.Shutdown()

	log.Info("VIPS initialized",
		zap.Int("max_cache_mb", cfg.VipsMaxCacheMB),
		zap.Int("concurrency", cfg.VipsConcurrency),
	)

	log.Info("Starting Gigaview server",
		zap.Int("port", cfg.Port),
		zap.String("data_dir", cfg.DataDir),
	)

	scanner := image_list.New(cfg.DataDir, log)
	if err := scanner.Scan(); err != nil {
		log.Warn("Initial scan failed", zap.Error(err))
	}

	tileCache, err := cache.NewCache(cfg.CacheType, cfg.CacheFileDir, cfg.CacheMemoryTiles, log)
	if err != nil {
		log.Fatal("Failed to initialize cache", zap.Error(err))
	}
	renderer := image_renderer.New(cfg.DataDir, scanner, tileCache, log)

	handlers := httphandlers.New(cfg, log, scanner, renderer)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/images", handlers.HandleImages)
	mux.HandleFunc("/api/images/", handlers.HandleImageRoutes)
	mux.HandleFunc("/api/upload", handlers.HandleUpload)
	mux.HandleFunc("/healthz", handlers.HandleHealthz)
	mux.HandleFunc("/", handlers.HandleStatic)

	handler := handlers.CORSMiddleware(handlers.RequestLoggingMiddleware(mux))

	if cfg.WarmupLevels > 0 {
		go warmupTiles(cfg.WarmupLevels, cfg.WarmupWorkers, scanner, renderer, log)
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: handler,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed", zap.Error(err))
		}
	}()

	log.Info("Server started", zap.Int("port", cfg.Port))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error("Server forced to shutdown", zap.Error(err))
	}

	log.Info("Server stopped")
}

func warmupTiles(levels int, workerLimit int, scanner *image_list.Scanner, renderer *image_renderer.Renderer, log *zap.Logger) {
	images := scanner.GetImages()
	if len(images) == 0 {
		return
	}

	log.Info("Starting tile warmup", zap.Int("levels", levels), zap.Int("images", len(images)))

	// Worker pool size configured via env (defaults to 1)
	if workerLimit <= 0 {
		workerLimit = 1
	}

	workerChan := make(chan struct{}, workerLimit)
	var wg sync.WaitGroup

	for _, img := range images {
		maxZoom := renderer.CalculateMaxZoom(img.Width, img.Height)
		warmupZoom := levels
		if warmupZoom > maxZoom {
			warmupZoom = maxZoom
		}

		for z := 0; z <= warmupZoom; z++ {
			tilesX := int(math.Ceil(float64(img.Width) / (256 * math.Pow(2, float64(maxZoom-z)))))
			tilesY := int(math.Ceil(float64(img.Height) / (256 * math.Pow(2, float64(maxZoom-z)))))

			for x := 0; x < tilesX; x++ {
				for y := 0; y < tilesY; y++ {
					wg.Add(1)
					workerChan <- struct{}{} // Acquire worker slot

					go func(imageID string, zoom, tileX, tileY int) {
						defer wg.Done()
						defer func() { <-workerChan }() // Release worker slot

						_, err := renderer.RenderTile(imageID, zoom, tileX, tileY)
						if err != nil {
							log.Debug("Warmup tile failed", zap.String("image", imageID), zap.Int("z", zoom), zap.Int("x", tileX), zap.Int("y", tileY), zap.Error(err))
						}
					}(img.ID, z, x, y)
				}
			}
		}
	}

	wg.Wait()
	log.Info("Tile warmup completed")
}
