package media

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/storage"
)

const maxUploadSize = 100 << 20 // 100 MB

type Handler struct {
	store *storage.DiskStore
}

func NewHandler(store *storage.DiskStore) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/upload", h.upload)
	r.Delete("/{category}/{filename}", h.delete)
	return r
}

type uploadResponse struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Category string `json:"category"`
	SizeKB   int    `json:"size_kb"`
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserID(r)
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "file too large or bad form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}

	// Read all bytes
	buf := make([]byte, header.Size)
	if _, err := file.Read(buf); err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}

	var (
		data     []byte
		ext      string
		category string
	)

	switch {
	case isImage(ct):
		category = "images"
		compressed, compExt, err := compress(buf, ct)
		if err != nil {
			http.Error(w, fmt.Sprintf("compress: %v", err), http.StatusBadRequest)
			return
		}
		data = compressed
		ext = compExt

	case isAudio(ct):
		category = "audio"
		data = buf
		ext = filepath.Ext(header.Filename)
		if ext == "" {
			ext = ".mp3"
		}

	default:
		// video or other — store as-is
		category = "videos"
		data = buf
		ext = filepath.Ext(header.Filename)
		if ext == "" {
			ext = ".mp4"
		}
	}

	filename := uuid.New().String() + ext
	url, err := h.store.Save(r.Context(), category, filename, data)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, uploadResponse{
		URL:      url,
		Filename: filename,
		Category: category,
		SizeKB:   len(data) / 1024,
	})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserID(r)
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	category := chi.URLParam(r, "category")
	filename := chi.URLParam(r, "filename")

	allowed := map[string]bool{"images": true, "audio": true, "videos": true}
	if !allowed[category] || strings.Contains(filename, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if err := h.store.Delete(r.Context(), category, filename); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
