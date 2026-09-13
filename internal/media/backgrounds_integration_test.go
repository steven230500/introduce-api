package media

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/db"
	"github.com/steven230500/introduce-api/internal/storage"
)

func TestBackgroundsAreHeldToTheStandard(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var userID, orgID uuid.UUID
	if err := pool.QueryRow(ctx, `insert into users (email, password_hash) values ($1, 'x') returning id`,
		uuid.NewString()+"@test.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into organizations (name) values ('Iglesia') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	store, err := storage.NewDisk(dir, "http://files.test")
	if err != nil {
		t.Fatal(err)
	}
	router := NewHandler(store, NewRepo(pool)).Router()

	send := func(method, path string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
		if body == nil {
			body = &bytes.Buffer{}
		}
		req := httptest.NewRequest(method, path, body)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req = req.WithContext(auth.WithIdentity(req.Context(), userID, orgID))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	type part struct {
		field, filename, contentType string
		data                         []byte
	}
	form := func(fields map[string]string, parts ...part) (*bytes.Buffer, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		for _, p := range parts {
			h := make(map[string][]string)
			h["Content-Disposition"] = []string{`form-data; name="` + p.field + `"; filename="` + p.filename + `"`}
			h["Content-Type"] = []string{p.contentType}
			w, _ := mw.CreatePart(h)
			_, _ = w.Write(p.data)
		}
		_ = mw.Close()
		return &buf, mw.FormDataContentType()
	}
	picture := func(w, h int) []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	decode := func(rec *httptest.ResponseRecorder) Item {
		var item Item
		if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
			t.Fatalf("unreadable response %d: %s", rec.Code, rec.Body.String())
		}
		return item
	}

	// A photo from a phone, the wrong way round: refused, and told why.
	body, ct := form(map[string]string{"role": "background"},
		part{"file", "vertical.png", "image/png", picture(720, 1280)})
	rec := send(http.MethodPost, "/upload", body, ct)
	if rec.Code != http.StatusUnprocessableEntity || !bytes.Contains(rec.Body.Bytes(), []byte("horizontal")) {
		t.Fatalf("portrait photo: %d %s", rec.Code, rec.Body.String())
	}

	// A Full HD still is kept as a background, with its size recorded.
	body, ct = form(map[string]string{"role": "background"},
		part{"file", "cruz.png", "image/png", picture(1920, 1080)})
	rec = send(http.MethodPost, "/upload", body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("full hd still: %d %s", rec.Code, rec.Body.String())
	}
	still := decode(rec)
	if still.Role != RoleBackground || still.MediaType != "image" ||
		still.Width == nil || *still.Width != 1920 || *still.Height != 1080 {
		t.Fatalf("still stored as %+v", still)
	}

	// A loop that restarts every second is refused before it is stored.
	body, ct = form(map[string]string{"role": "background", "width": "1920", "height": "1080", "duration_ms": "1000"},
		part{"file", "blink.mp4", "video/mp4", []byte("not really a video")})
	rec = send(http.MethodPost, "/upload", body, ct)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("one-second loop: %d %s", rec.Code, rec.Body.String())
	}

	// A proper loop, with a still frame of itself.
	video := bytes.Repeat([]byte{7}, 4096)
	body, ct = form(map[string]string{"role": "background", "width": "1920", "height": "1080", "duration_ms": "30000"},
		part{"file", "olas.mp4", "video/mp4", video},
		part{"poster", "olas.jpg", "image/png", picture(1920, 1080)})
	rec = send(http.MethodPost, "/upload", body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("loop: %d %s", rec.Code, rec.Body.String())
	}
	loop := decode(rec)
	if loop.MediaType != "video" || loop.DurationMs == nil || *loop.DurationMs != 30000 || loop.PosterURL == nil {
		t.Fatalf("loop stored as %+v", loop)
	}
	if *loop.SizeBytes <= int64(len(video)) {
		t.Fatalf("the still frame is not counted against storage: %d", *loop.SizeBytes)
	}

	// The backgrounds are listed as backgrounds, and kept out of the library
	// an operator puts things on the screen from.
	var listed []Item
	_ = json.Unmarshal(send(http.MethodGet, "/backgrounds", nil, "").Body.Bytes(), &listed)
	if len(listed) != 2 {
		t.Fatalf("backgrounds listed %d, want 2", len(listed))
	}
	_ = json.Unmarshal(send(http.MethodGet, "/", nil, "").Body.Bytes(), &listed)
	if len(listed) != 0 {
		t.Fatalf("library lists %d backgrounds among its files", len(listed))
	}

	// Deleting the loop deletes the still frame with it.
	if rec := send(http.MethodDelete, "/item/"+loop.ID.String(), nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	for _, stored := range []string{loop.StoragePath} {
		if _, err := os.Stat(filepath.Join(dir, stored)); !os.IsNotExist(err) {
			t.Fatalf("%s left on disk", stored)
		}
	}
	posters, _ := os.ReadDir(filepath.Join(dir, "images"))
	if len(posters) != 1 {
		t.Fatalf("images on disk after deleting the loop: %d, want only the still background", len(posters))
	}

	// An MP4 sent to the library the ordinary way is a video, not audio and not
	// an image.
	body, ct = form(nil, part{"file", "anuncio.mp4", "video/mp4", video})
	rec = send(http.MethodPost, "/upload", body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("library mp4: %d %s", rec.Code, rec.Body.String())
	}
	if clip := decode(rec); clip.MediaType != "video" || clip.Role != RoleLibrary {
		t.Fatalf("library mp4 stored as %+v", clip)
	}
}
