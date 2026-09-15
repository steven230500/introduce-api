package stats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/steven230500/introduce-api/internal/auth"
)

type fakeEvents struct{ recorded []Event }

func (f *fakeEvents) Record(_ context.Context, e Event) error {
	f.recorded = append(f.recorded, e)
	return nil
}

type fakeSummary struct{ summary Summary }

func (f fakeSummary) Summarize(context.Context) (Summary, error) { return f.summary, nil }

type fakeReleases struct{}

func (fakeReleases) AssetURL(_ context.Context, platform string) string {
	return "https://github.com/x/y/releases/download/v1.2.0/Introduce-" + platform + "-1.2.0.zip"
}

func newTestHandler(password string, summary Summary) (*Handler, *fakeEvents, http.Handler) {
	events := &fakeEvents{}
	h := &Handler{events: events, summary: fakeSummary{summary}, releases: fakeReleases{}, password: password}
	r := chi.NewRouter()
	h.Register(r)
	return h, events, r
}

func TestAPersonPressingDownloadIsCountedAndSentToTheZip(t *testing.T) {
	_, events, router := newTestHandler("", Summary{})
	req := httptest.NewRequest(http.MethodGet, "/download/mac", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) Safari/605.1.15")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("status %d, want 302", res.Code)
	}
	if got := res.Header().Get("Location"); !strings.HasSuffix(got, "Introduce-macos-1.2.0.zip") {
		t.Fatalf("redirected to %q", got)
	}
	if len(events.recorded) != 1 || events.recorded[0].Name != "download" || events.recorded[0].Platform != "macos" {
		t.Fatalf("recorded %+v", events.recorded)
	}
}

func TestACrawlerFollowingTheButtonIsNotCounted(t *testing.T) {
	_, events, router := newTestHandler("", Summary{})
	req := httptest.NewRequest(http.MethodGet, "/download/windows", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("status %d: the crawler still gets the file", res.Code)
	}
	if len(events.recorded) != 0 {
		t.Fatalf("a crawler was counted: %+v", events.recorded)
	}
}

func TestAPlatformThatDoesNotExistIsNotFound(t *testing.T) {
	_, events, router := newTestHandler("", Summary{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/download/amiga", nil))

	if res.Code != http.StatusNotFound || len(events.recorded) != 0 {
		t.Fatalf("status %d, recorded %d", res.Code, len(events.recorded))
	}
}

func postEvent(router http.Handler, body string, ctx context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body)).WithContext(ctx)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

func TestAnOpenedCopyFromASignedInChurchCarriesTheChurch(t *testing.T) {
	_, events, router := newTestHandler("", Summary{})
	orgID := uuid.New()
	install := uuid.New()
	ctx := auth.WithIdentity(context.Background(), uuid.New(), orgID)

	res := postEvent(router,
		`{"name":"app_open","platform":"windows","app_version":"1.0.2","install_id":"`+install.String()+`"}`, ctx)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	got := events.recorded[0]
	if got.OrgID == nil || *got.OrgID != orgID || got.InstallID == nil || *got.InstallID != install {
		t.Fatalf("recorded %+v", got)
	}
	if got.AppVersion != "1.0.2" || got.Platform != "windows" {
		t.Fatalf("recorded %+v", got)
	}
}

func TestAnOpenedCopyBeforeSigningInIsCountedWithoutAChurch(t *testing.T) {
	_, events, router := newTestHandler("", Summary{})

	res := postEvent(router,
		`{"name":"app_open","platform":"macos","app_version":"1.0.2","install_id":"`+uuid.NewString()+`"}`,
		context.Background())

	if res.Code != http.StatusNoContent || events.recorded[0].OrgID != nil {
		t.Fatalf("status %d, recorded %+v", res.Code, events.recorded)
	}
}

func TestOnlyTheEventsTheServerKnowsAreAccepted(t *testing.T) {
	_, events, router := newTestHandler("", Summary{})
	install := uuid.NewString()

	for _, body := range []string{
		`{"name":"clicked_everything","platform":"macos","app_version":"1.0.2","install_id":"` + install + `"}`,
		`{"name":"app_open","platform":"toaster","app_version":"1.0.2","install_id":"` + install + `"}`,
		`{"name":"app_open","platform":"macos","app_version":"<script>","install_id":"` + install + `"}`,
		`{"name":"app_open","platform":"macos","app_version":"1.0.2"}`,
		`{"name":"app_open","platform":"macos","app_version":"1.0.2","install_id":"` + install + `","email":"x@y.z"}`,
	} {
		if res := postEvent(router, body, context.Background()); res.Code != http.StatusBadRequest {
			t.Errorf("status %d for %s", res.Code, body)
		}
	}
	if len(events.recorded) != 0 {
		t.Fatalf("recorded %+v", events.recorded)
	}
}

func TestWithNoPasswordSetTheDashboardDoesNotExist(t *testing.T) {
	_, _, router := newTestHandler("", Summary{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/stats", nil))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", res.Code)
	}
}

func TestTheDashboardAsksForThePassword(t *testing.T) {
	_, _, router := newTestHandler("una-clave-larga", Summary{})

	for _, password := range []string{"", "otra-clave"} {
		req := httptest.NewRequest(http.MethodGet, "/stats", nil)
		if password != "" {
			req.SetBasicAuth("", password)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized || res.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("password %q: status %d", password, res.Code)
		}
	}
}

func TestTheDashboardNamesCountriesInSpanishAndListsChurches(t *testing.T) {
	last := time.Date(2026, 9, 13, 15, 0, 0, 0, time.UTC)
	_, _, router := newTestHandler("una-clave-larga", Summary{
		Downloads:          Windows{Week: 3, Month: 5, Total: 9},
		DownloadsByCountry: []Row{{Label: "CO", Windows: Windows{Week: 2, Month: 4, Total: 7}}, {Label: ""}},
		Churches: []Church{{
			Name: "Iglesia <Central>", Country: "MX", CreatedAt: last, Members: 4, Pending: 1,
			Admins: "pastor@example.com", LastProjection: &last, DaysProjected: 2,
		}},
	})
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	req.SetBasicAuth("", "una-clave-larga")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	body := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, body)
	}
	for _, want := range []string{"Colombia", "México", "Sin identificar", "pastor@example.com", "13 sep 2026", "Iglesia &lt;Central&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard does not show %q", want)
		}
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Error("the dashboard may be cached")
	}
}
