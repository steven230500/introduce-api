// Package config reads the process environment once, at startup.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Port          string
	DatabaseURL   string
	UploadsDir    string
	FilesBaseURL  string
	JWTSecret     []byte
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	AllowedOrigin string

	// Password for the owner's dashboard at /stats. Empty turns it off.
	StatsPassword string
	// The free DB-IP country database. Missing means blank countries.
	GeoIPDatabase string
	// Where the app's releases live, for the website's download buttons.
	ReleasesRepo string
}

// Load returns the configuration, or an error naming every missing setting at
// once so a bad deploy is fixed in one pass instead of one restart per variable.
func Load() (Config, error) {
	cfg := Config{
		Port:          env("PORT", "8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		UploadsDir:    env("UPLOADS_DIR", "./uploads"),
		FilesBaseURL:  env("FILES_BASE_URL", "http://localhost:8080/files"),
		JWTSecret:     []byte(os.Getenv("JWT_SECRET")),
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    30 * 24 * time.Hour,
		AllowedOrigin: env("ALLOWED_ORIGIN", "*"),
		StatsPassword: os.Getenv("STATS_PASSWORD"),
		GeoIPDatabase: env("GEOIP_DATABASE", "./dbip-country-lite.mmdb"),
		ReleasesRepo:  env("RELEASES_REPO", "steven230500/introduce-church"),
	}

	var missing []string
	if cfg.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	// 32 bytes of entropy is the floor for HS256. A short secret is the kind of
	// mistake that never surfaces until someone forges a token.
	if len(cfg.JWTSecret) < 32 {
		missing = append(missing, "JWT_SECRET (at least 32 characters)")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing configuration: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
