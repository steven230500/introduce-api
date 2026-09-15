package stats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestTheButtonGetsTheZipForItsPlatformFromTheLatestRelease(t *testing.T) {
	var calls atomic.Int32
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/repos/owner/app/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"assets":[
			{"name":"Introduce-macOS-1.2.0.zip","browser_download_url":"https://dl/mac.zip"},
			{"name":"Introduce-Windows-1.2.0.zip","browser_download_url":"https://dl/win.zip"},
			{"name":"checksums.txt","browser_download_url":"https://dl/sums.txt"}]}`))
	}))
	defer github.Close()

	releases := NewReleases("owner/app")
	releases.apiBase = github.URL
	ctx := context.Background()

	if got := releases.AssetURL(ctx, "macos"); got != "https://dl/mac.zip" {
		t.Fatalf("macos: %q", got)
	}
	if got := releases.AssetURL(ctx, "windows"); got != "https://dl/win.zip" {
		t.Fatalf("windows: %q", got)
	}
	// Ten presses in a minute are one question to GitHub, not ten.
	if calls.Load() != 1 {
		t.Fatalf("asked GitHub %d times", calls.Load())
	}
}

func TestWhenGitHubCannotBeReachedTheButtonStillLeadsToTheRelease(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer github.Close()

	releases := NewReleases("owner/app")
	releases.apiBase = github.URL

	if got := releases.AssetURL(context.Background(), "macos"); got != "https://github.com/owner/app/releases/latest" {
		t.Fatalf("got %q", got)
	}
}
