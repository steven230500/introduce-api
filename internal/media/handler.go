package media

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/httpx"
	"github.com/steven230500/introduce-api/internal/plan"
	"github.com/steven230500/introduce-api/internal/storage"
)

// The ceiling no plan may cross, so a malformed or hostile request cannot make
// the process hold two gigabytes of someone else's memory while the per-plan
// limit is being looked up.
const hardUploadCeiling = 2 << 30 // 2 GB

type Handler struct {
	store storage.Store
	repo  *Repo
}

func NewHandler(store storage.Store, repo *Repo) *Handler {
	return &Handler{store: store, repo: repo}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Get("/usage", h.usage)
	r.Post("/upload", h.upload)
	r.Delete("/item/{id}", h.deleteItem)
	r.Delete("/{category}/{filename}", h.delete)
	return r
}

type usageResponse struct {
	Plan       plan.Name `json:"plan"`
	Label      string    `json:"label"`
	UsedBytes  int64     `json:"used_bytes"`
	TotalBytes int64     `json:"total_bytes"`
	MaxUpload  int64     `json:"max_upload_bytes"`
}

// usage tells the app how much room is left, so the library can show it before
// somebody spends four minutes uploading a video that will be refused.
func (h *Handler) usage(w http.ResponseWriter, r *http.Request) {
	used, planName, err := h.repo.Usage(r.Context(), auth.MustOrgID(r.Context()))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	limits := plan.For(planName)
	httpx.JSON(w, http.StatusOK, usageResponse{
		Plan:       limits.Name,
		Label:      limits.Label,
		UsedBytes:  used,
		TotalBytes: limits.Storage,
		MaxUpload:  limits.MaxUpload,
	})
}

type uploadResponse struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Category string `json:"category"`
	SizeKB   int    `json:"size_kb"`
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserID(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	orgID := auth.MustOrgID(r.Context())
	used, planName, err := h.repo.Usage(r.Context(), orgID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	limits := plan.For(planName)

	r.Body = http.MaxBytesReader(w, r.Body, hardUploadCeiling)
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

	// Both checks happen before the bytes are read into memory, and both say
	// the number in the message: "too large" with no size is a dead end for
	// whoever is standing at the machine.
	if header.Size > limits.MaxUpload {
		httpx.WriteError(w, httpx.Fail(http.StatusRequestEntityTooLarge, "file_too_large",
			fmt.Sprintf("El archivo pesa %s y en el plan %s cada archivo puede pesar hasta %s.",
				plan.Human(header.Size), limits.Label, plan.Human(limits.MaxUpload))))
		return
	}
	if used+header.Size > limits.Storage {
		httpx.WriteError(w, httpx.Fail(http.StatusInsufficientStorage, "storage_full",
			fmt.Sprintf("El plan %s tiene %s y ya hay %s usados. Borra algo de la biblioteca o pasa a un plan más grande.",
				limits.Label, plan.Human(limits.Storage), plan.Human(used))))
		return
	}

	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}

	// io.ReadAll, not one Read into a sized buffer: Read is allowed to return
	// fewer bytes than asked for, and the file that gets truncated that way is
	// the large one, which is the one nobody wants to upload twice.
	buf, err := io.ReadAll(file)
	if err != nil {
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
	url, err := h.store.Save(r.Context(), category, filename, data, storedType(category, ct, ext))
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	// Index the file so the library can list it. The upload already succeeded,
	// so a failure here loses the row but not the bytes, and the client is told.
	userID, _ := auth.UserID(r.Context())
	size := int64(len(data))
	mediaType := "image"
	if category == "videos" {
		mediaType = "video"
	}

	item, err := h.repo.Create(r.Context(), orgID, userID, Item{
		Name:        displayName(header.Filename),
		URL:         url,
		StoragePath: category + "/" + filename,
		MediaType:   mediaType,
		SizeBytes:   &size,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, item)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.repo.List(r.Context(), auth.MustOrgID(r.Context()))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// deleteItem removes the library entry and the file behind it.
func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_id", "id inválido"))
		return
	}

	item, err := h.repo.Take(r.Context(), auth.MustOrgID(r.Context()), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	parts := strings.SplitN(item.StoragePath, "/", 2)
	if len(parts) == 2 {
		// A missing file is not an error worth failing the request over: the
		// row is already gone and the library is consistent.
		_ = h.store.Delete(r.Context(), parts[0], parts[1])
	}
	httpx.NoContent(w)
}

// storedType is the content type the file is served back with. Compression may
// have changed the format underneath, so the type the browser was told on the
// way in is not necessarily the one to keep.
func storedType(category, uploaded, ext string) string {
	if category != "images" {
		return uploaded
	}
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return uploaded
	}
}

// displayName strips the extension, which is noise in a library listing.
func displayName(filename string) string {
	base := filepath.Base(filename)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return "archivo"
	}
	return base
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserID(r.Context()); !ok {
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
