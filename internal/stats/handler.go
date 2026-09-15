package stats

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/geo"
	"github.com/steven230500/introduce-api/internal/httpx"
)

type recorder interface {
	Record(ctx context.Context, e Event) error
}

type summarizer interface {
	Summarize(ctx context.Context) (Summary, error)
}

type assetFinder interface {
	AssetURL(ctx context.Context, platform string) string
}

type Handler struct {
	events   recorder
	summary  summarizer
	releases assetFinder
	geo      *geo.Locator
	// The dashboard's password. Empty turns the dashboard off.
	password string
}

func NewHandler(repo *Repo, releases *Releases, locator *geo.Locator, password string) *Handler {
	return &Handler{events: repo, summary: repo, releases: releases, geo: locator, password: password}
}

// Register adds what anyone can reach: the download buttons, the app's report
// that it was opened, and the password-protected dashboard. The router should
// run [auth.Service.Identify], so a signed-in app's report carries its church.
func (h *Handler) Register(r chi.Router) {
	r.Get("/download/{platform}", h.download)
	r.Post("/events", h.event)
	r.Get("/stats", h.dashboard)
}

// Machines follow links too. A search engine walking the download buttons
// would otherwise count as a church in whatever country its servers are.
var botAgent = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|preview|fetch|monitor|curl|wget|python|go-http|headless`)

func platformFrom(param string) (string, bool) {
	switch strings.ToLower(param) {
	case "mac", "macos":
		return "macos", true
	case "windows", "win":
		return "windows", true
	}
	return "", false
}

// download counts one press of a download button and sends the browser on to
// the zip. Counting never stands in the way of the download: if the insert
// fails the redirect still happens.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	platform, ok := platformFrom(chi.URLParam(r, "platform"))
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}

	if !botAgent.MatchString(r.UserAgent()) {
		err := h.events.Record(r.Context(), Event{
			Name:     "download",
			Platform: platform,
			Country:  h.geo.Country(geo.ClientIP(r)),
		})
		if err != nil {
			log.Printf("stats: record download: %v", err)
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, h.releases.AssetURL(r.Context(), platform), http.StatusFound)
}

var versionPattern = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)

// event takes the app's report that a copy was opened. The only name accepted
// is app_open: this is not a general tracking endpoint, and a new kind of event
// should be a decision made here, not something a client invents.
func (h *Handler) event(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string    `json:"name"`
		Platform   string    `json:"platform"`
		AppVersion string    `json:"app_version"`
		InstallID  uuid.UUID `json:"install_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if body.Name != "app_open" {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "unknown_event", "evento desconocido"))
		return
	}
	switch body.Platform {
	case "macos", "windows", "linux":
	default:
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "invalid_platform", "plataforma inválida"))
		return
	}
	if !versionPattern.MatchString(body.AppVersion) || body.InstallID == uuid.Nil {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_request", "faltan datos"))
		return
	}

	installID := body.InstallID
	e := Event{
		Name:       body.Name,
		Platform:   body.Platform,
		AppVersion: body.AppVersion,
		Country:    h.geo.Country(geo.ClientIP(r)),
		InstallID:  &installID,
	}
	if orgID, ok := auth.OrgID(r.Context()); ok {
		e.OrgID = &orgID
	}
	if err := h.events.Record(r.Context(), e); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

// dashboard is the owner's view, behind HTTP Basic auth. With no password set
// it does not exist, rather than being open.
func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	if h.password == "" {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	_, given, ok := r.BasicAuth()
	if !ok || !samePassword(given, h.password) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Introduce", charset="UTF-8"`)
		http.Error(w, "Hace falta la contraseña.", http.StatusUnauthorized)
		return
	}

	summary, err := h.summary.Summarize(r.Context())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	if err := dashboardPage.Execute(w, summary); err != nil {
		log.Printf("stats: render dashboard: %v", err)
	}
}

// Hashing first makes the comparison take the same time whatever the lengths.
func samePassword(given, want string) bool {
	a := sha256.Sum256([]byte(given))
	b := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
