package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"gigaview/internal/config"
	"gigaview/internal/image_list"
	"gigaview/internal/image_renderer"
)

type Handlers struct {
	config   *config.Config
	logger   *zap.Logger
	scanner  *image_list.Scanner
	renderer *image_renderer.Renderer
}

func New(config *config.Config, logger *zap.Logger, scanner *image_list.Scanner, renderer *image_renderer.Renderer) *Handlers {
	return &Handlers{
		config:   config,
		logger:   logger,
		scanner:  scanner,
		renderer: renderer,
	}
}

func (h *Handlers) RequestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		start := time.Now()

		ip := h.extractIP(r)

		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		bytes := wrapped.bytesWritten

		h.logger.Info("request",
			zap.String("request_id", requestID),
			zap.String("ip", ip),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", wrapped.statusCode),
			zap.Int64("bytes", bytes),
			zap.Int64("duration_ms", duration.Milliseconds()),
			zap.String("user_agent", r.UserAgent()),
		)
	})
}

func (h *Handlers) CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigin := ""

		if h.config.AllowedOrigin != "" {
			allowedOrigin = h.config.AllowedOrigin
		} else {
			host := r.Host
			if origin != "" && strings.HasPrefix(origin, "http://"+host) || strings.HasPrefix(origin, "https://"+host) {
				allowedOrigin = origin
			} else if origin == "" {
				allowedOrigin = "*"
			}
		}

		if allowedOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (h *Handlers) HandleImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	images := h.scanner.GetImages()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(images)
}

func (h *Handlers) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.config.IsUploadPublic() {
		token := ""
		if authHeader := r.Header.Get("Authorization"); authHeader != "" {
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}
		if token == "" {
			token = r.URL.Query().Get("token")
		}

		if token != h.config.UploadToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.config.MaxUploadSize)

	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		http.Error(w, "Failed to parse multipart form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{
		".tif":  true,
		".tiff": true,
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".webp": true,
	}

	if !allowedExts[ext] {
		http.Error(w, "Invalid file extension", http.StatusBadRequest)
		return
	}

	tempFile, err := os.CreateTemp(os.TempDir(), "upload_*"+ext)
	if err != nil {
		h.logger.Error("Failed to create temp file", zap.Error(err))
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	tempPath := tempFile.Name()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		h.logger.Error("Failed to copy file", zap.Error(err))
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	tempFile.Close()

	imageID, err := h.scanner.ProcessUploadedFile(tempPath, header.Filename)
	if err != nil {
		if _, statErr := os.Stat(tempPath); statErr == nil {
			os.Remove(tempPath)
		}
		h.logger.Error("Failed to process uploaded file", zap.Error(err))
		http.Error(w, "Failed to process file", http.StatusInternalServerError)
		return
	}

	err = h.scanner.Scan()
	if err != nil {
		h.logger.Warn("Failed to rescan after upload", zap.Error(err))
	}

	// Get image info for response
	imageInfo := h.scanner.GetImageByID(imageID)
	if imageInfo == nil {
		h.logger.Warn("Uploaded image not found after scan", zap.String("id", imageID))
		http.Error(w, "Failed to retrieve uploaded image", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"id":    imageID,
		"name":  imageInfo.OriginalFilename,
		"saved": true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *Handlers) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (h *Handlers) HandleImageRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/images/")
	parts := strings.Split(strings.Trim(path, "/"), "/")

	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}

	imageID := parts[0]

	switch {
	case len(parts) == 2 && parts[1] == "meta":
		h.handleImageMetaWithID(w, r, imageID)
	case len(parts) >= 5 && parts[1] == "tiles":
		h.handleTileWithParams(w, r, imageID, parts[2:])
	default:
		http.NotFound(w, r)
	}
}

func (h *Handlers) HandleStatic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	filePath := filepath.Join("public", path)

	if !strings.HasPrefix(filepath.Clean(filePath), "public") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// If serving index.html, replace the placeholder with the actual base URL
	if path == "/index.html" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		content := strings.ReplaceAll(string(data), "__PUBLIC_BASE_URL__", h.config.PublicBaseURL)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(content))
		return
	}

	http.ServeFile(w, r, filePath)
}

func (h *Handlers) handleImageMetaWithID(w http.ResponseWriter, r *http.Request, imageID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	meta, err := h.renderer.GetImageMeta(imageID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

func (h *Handlers) handleTileWithParams(w http.ResponseWriter, r *http.Request, imageID string, tileParts []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if len(tileParts) < 3 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	var z, x, y int
	if _, err := fmt.Sscanf(tileParts[0], "%d", &z); err != nil {
		http.Error(w, "Invalid zoom level", http.StatusBadRequest)
		return
	}
	if _, err := fmt.Sscanf(tileParts[1], "%d", &x); err != nil {
		http.Error(w, "Invalid x coordinate", http.StatusBadRequest)
		return
	}

	tileFile := tileParts[2]
	ext := filepath.Ext(tileFile)
	if _, err := fmt.Sscanf(strings.TrimSuffix(tileFile, ext), "%d", &y); err != nil {
		http.Error(w, "Invalid y coordinate", http.StatusBadRequest)
		return
	}

	if z < 0 || x < 0 || y < 0 {
		http.Error(w, "Coordinates must be non-negative", http.StatusBadRequest)
		return
	}

	format := strings.TrimPrefix(ext, ".")
	if format != "jpg" && format != "jpeg" && format != "webp" {
		http.Error(w, "Invalid format", http.StatusBadRequest)
		return
	}

	if format == "jpg" {
		format = "jpeg"
	}

	result, err := h.renderer.RenderTile(imageID, z, x, y)
	if err != nil {
		h.logger.Error("Failed to render tile", zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("ETag", `"`+result.ETag+`"`)
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", result.Size))
	w.Header().Set("X-Tile-Bytes", fmt.Sprintf("%d", result.Size))

	contentType := "image/jpeg"
	if format == "webp" {
		contentType = "image/webp"
	}
	w.Header().Set("Content-Type", contentType)

	// HEAD request doesn't send body
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Write(result.Data)
}

// Not for real production use due to potential spoofing
// but it's fine for a demo
func (h *Handlers) extractIP(r *http.Request) string {
	ip := r.Header.Get("X-Real-Ip")
	if ip != "" {
		return strings.Split(ip, ":")[0]
	}

	addr := r.RemoteAddr
	if addr != "" {
		return strings.Split(addr, ":")[0]
	}

	return "unknown"
}

type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}
