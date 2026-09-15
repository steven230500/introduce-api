package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Releases finds the zip a download button should hand out: the one for that
// platform in the latest GitHub release. Asset names carry the version, so a
// fixed link cannot point at them.
type Releases struct {
	repo   string
	client *http.Client
	// Where GitHub's API lives; a test points it at its own server.
	apiBase string

	mu      sync.Mutex
	assets  map[string]string
	fetched time.Time
}

// How long one answer from GitHub is reused. Unauthenticated calls are limited
// to 60 an hour, and a release is published a few times a month at most.
const releasesTTL = 10 * time.Minute

func NewReleases(repo string) *Releases {
	return &Releases{
		repo:    repo,
		client:  &http.Client{Timeout: 5 * time.Second},
		apiBase: "https://api.github.com",
	}
}

// PageURL is the latest release on GitHub, where every file is listed.
func (r *Releases) PageURL() string {
	return "https://github.com/" + r.repo + "/releases/latest"
}

// AssetURL is the zip for platform ("macos" or "windows"). When GitHub cannot
// be reached and nothing was fetched before, it is the release page instead:
// one more click is better than a broken button.
func (r *Releases) AssetURL(ctx context.Context, platform string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.assets == nil || time.Since(r.fetched) > releasesTTL {
		if assets, err := r.fetch(ctx); err == nil {
			r.assets = assets
			r.fetched = time.Now()
		}
	}
	if url, ok := r.assets[platform]; ok {
		return url
	}
	return r.PageURL()
}

func (r *Releases) fetch(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.apiBase+"/repos/"+r.repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: %s", res.Status)
	}

	var release struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(res.Body).Decode(&release); err != nil {
		return nil, err
	}
	assets := map[string]string{}
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasSuffix(name, ".zip") {
			continue
		}
		for _, platform := range []string{"macos", "windows"} {
			if strings.Contains(name, platform) {
				assets[platform] = asset.URL
			}
		}
	}
	return assets, nil
}
