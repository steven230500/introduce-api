package media

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	r.Get("/backgrounds", h.backgrounds)
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

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
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
		httpx.WriteError(w, storageFull(limits, used))
		return
	}

	role := r.FormValue("role")
	if role == "" {
		role = RoleLibrary
	}
	if role != RoleLibrary && role != RoleBackground {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_role", "role inválido"))
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

	item := Item{Name: displayName(header.Filename), Role: role}
	category := categoryFor(ct)

	if role == RoleBackground {
		// A background's kind comes from its extension, which the standard
		// names, rather than from a content type the client library guessed.
		kind := backgroundKind(header.Filename)
		candidate := Candidate{Filename: header.Filename, Bytes: header.Size}
		switch kind {
		case "image":
			category = "images"
			// The header of the image, not the whole picture: enough to know
			// its size without decoding forty megapixels to find out.
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(buf)); err == nil {
				candidate.Width, candidate.Height = cfg.Width, cfg.Height
			}
		case "video":
			category = "videos"
			// The server has no video decoder, so the app says how large and
			// how long the loop is. It has already checked the file itself;
			// this keeps a client that did not from filing nonsense.
			candidate.Width = formInt(r, "width")
			candidate.Height = formInt(r, "height")
			candidate.Duration = time.Duration(formInt(r, "duration_ms")) * time.Millisecond
		}
		if problems := CheckBackground(candidate); len(problems) > 0 {
			httpx.WriteError(w, httpx.Fail(http.StatusUnprocessableEntity, "background_standard",
				strings.Join(problems, " ")))
			return
		}
		item.Width, item.Height = &candidate.Width, &candidate.Height
		if kind == "video" {
			ms := int(candidate.Duration / time.Millisecond)
			item.DurationMs = &ms
		}
	}

	var (
		data []byte
		ext  string
	)
	switch category {
	case "images":
		compressed, compExt, err := compress(buf, ct)
		if err != nil {
			http.Error(w, fmt.Sprintf("compress: %v", err), http.StatusBadRequest)
			return
		}
		data, ext = compressed, compExt
		if role == RoleBackground {
			// Scaling down on upload changed the size that was checked; the
			// row records what is actually stored.
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
				item.Width, item.Height = &cfg.Width, &cfg.Height
			}
		}
	case "audio":
		data = buf
		ext = filepath.Ext(header.Filename)
		if ext == "" {
			ext = ".mp3"
		}
	default:
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
	size := int64(len(data))

	// A still frame of a video background, sent alongside it. Optional: a
	// background without one shows its colour where a thumbnail would be,
	// which is worse but not broken, and not worth refusing the loop over.
	if role == RoleBackground && category == "videos" {
		if posterURL, posterPath, posterSize, ok := h.savePoster(r); ok {
			item.PosterURL, item.PosterPath = &posterURL, &posterPath
			size += posterSize
		}
	}

	// Index the file so the library can list it. The upload already succeeded,
	// so a failure here loses the row but not the bytes, and the client is told.
	item.URL = url
	item.StoragePath = category + "/" + filename
	item.MediaType = "image"
	if category == "videos" {
		item.MediaType = "video"
	}
	item.SizeBytes = &size

	created, err := h.repo.Create(r.Context(), orgID, userID, item)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, created)
}

// savePoster stores the "poster" part of the form, if there is one and it is a
// picture.
func (h *Handler) savePoster(r *http.Request) (url, path string, size int64, ok bool) {
	part, header, err := r.FormFile("poster")
	if err != nil {
		return "", "", 0, false
	}
	defer part.Close()
	raw, err := io.ReadAll(part)
	if err != nil {
		return "", "", 0, false
	}
	data, ext, err := compress(raw, header.Header.Get("Content-Type"))
	if err != nil {
		return "", "", 0, false
	}
	filename := uuid.New().String() + ext
	url, err = h.store.Save(r.Context(), "images", filename, data, storedType("images", "", ext))
	if err != nil {
		return "", "", 0, false
	}
	return url, "images/" + filename, int64(len(data)), true
}

func storageFull(limits plan.Limits, used int64) error {
	return httpx.Fail(http.StatusInsufficientStorage, "storage_full",
		fmt.Sprintf("El plan %s tiene %s y ya hay %s usados. Borra algo de la biblioteca o pasa a un plan más grande.",
			limits.Label, plan.Human(limits.Storage), plan.Human(used)))
}

func formInt(r *http.Request, key string) int {
	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue(key)))
	if err != nil {
		return 0
	}
	return n
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	h.listRole(w, r, RoleLibrary)
}

// backgrounds lists what a church has added to put behind its designs.
func (h *Handler) backgrounds(w http.ResponseWriter, r *http.Request) {
	h.listRole(w, r, RoleBackground)
}

func (h *Handler) listRole(w http.ResponseWriter, r *http.Request, role string) {
	out, err := h.repo.List(r.Context(), auth.MustOrgID(r.Context()), role)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// deleteItem removes the library entry and the files behind it.
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

	// A missing file is not an error worth failing the request over: the row
	// is already gone and the library is consistent.
	for _, stored := range []*string{&item.StoragePath, item.PosterPath} {
		if stored == nil {
			continue
		}
		if parts := strings.SplitN(*stored, "/", 2); len(parts) == 2 {
			_ = h.store.Delete(r.Context(), parts[0], parts[1])
		}
	}
	httpx.NoContent(w)
}

// categoryFor files an upload by the content type it arrived with.
//
// Video is asked about before audio, and audio by its prefix: "video/mp4"
// contains "mp4", and a check for that alone filed every MP4 as audio.
func categoryFor(contentType string) string {
	ct := strings.ToLower(contentType)
	switch {
	case isImage(ct):
		return "images"
	case strings.HasPrefix(ct, "video/"):
		return "videos"
	case isAudio(ct):
		return "audio"
	default:
		// Video or anything else is stored as-is.
		return "videos"
	}
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
