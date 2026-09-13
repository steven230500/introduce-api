package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Store keeps uploads in any S3-compatible bucket.
//
// "S3-compatible" rather than AWS on purpose: DigitalOcean Spaces, Cloudflare
// R2 and Backblaze B2 all speak the same protocol at a fraction of the price,
// and R2 in particular charges nothing for the bytes read back, which for a
// church that projects the same video every Sunday is the bill that matters.
type S3Store struct {
	client *minio.Client
	bucket string

	// Where a browser reads the file from. A bucket behind a CDN or a custom
	// domain is not reachable at the API endpoint, so this is configured
	// separately rather than derived.
	publicBase string
}

// S3Config is the environment an S3Store needs. Empty Endpoint means the
// deployment has not been given a bucket and should stay on disk.
type S3Config struct {
	Endpoint   string // "nyc3.digitaloceanspaces.com", "<id>.r2.cloudflarestorage.com"
	Region     string
	Bucket     string
	AccessKey  string
	SecretKey  string
	PublicBase string // "https://cdn.example.com" or the bucket's own URL
}

// S3FromEnv reads the bucket settings. It returns the zero value with no error
// when none are set, which is how a local or self-hosted deployment stays on
// disk without special-casing anything.
func S3FromEnv() S3Config {
	return S3Config{
		Endpoint:   os.Getenv("S3_ENDPOINT"),
		Region:     envOr("S3_REGION", "auto"),
		Bucket:     os.Getenv("S3_BUCKET"),
		AccessKey:  os.Getenv("S3_ACCESS_KEY"),
		SecretKey:  os.Getenv("S3_SECRET_KEY"),
		PublicBase: strings.TrimRight(os.Getenv("S3_PUBLIC_BASE"), "/"),
	}
}

// Configured says whether the deployment was given a bucket at all.
func (c S3Config) Configured() bool { return c.Endpoint != "" }

// Validate names everything missing at once, so a half-filled bucket
// configuration is fixed in one pass instead of one restart per variable.
func (c S3Config) Validate() error {
	var missing []string
	for name, value := range map[string]string{
		"S3_BUCKET":      c.Bucket,
		"S3_ACCESS_KEY":  c.AccessKey,
		"S3_SECRET_KEY":  c.SecretKey,
		"S3_PUBLIC_BASE": c.PublicBase,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("S3_ENDPOINT is set but %s %s missing", strings.Join(sorted(missing), ", "), plural(len(missing)))
	}
	if _, err := url.Parse(c.PublicBase); err != nil {
		return errors.New("S3_PUBLIC_BASE is not a URL")
	}
	return nil
}

func NewS3(cfg S3Config) (*S3Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: true,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	return &S3Store{client: client, bucket: cfg.Bucket, publicBase: cfg.PublicBase}, nil
}

func (s *S3Store) Kind() string { return "s3" }

func (s *S3Store) Save(ctx context.Context, category, filename string, data []byte, contentType string) (string, error) {
	key := category + "/" + filename
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
		// A slide background does not change once uploaded: the name carries a
		// UUID, so a new file is a new URL. Cached for a year, the projector
		// machine fetches each one once.
		CacheControl: "public, max-age=31536000, immutable",
	})
	if err != nil {
		return "", fmt.Errorf("put %s: %w", key, err)
	}
	return s.publicBase + "/" + key, nil
}

func (s *S3Store) Delete(ctx context.Context, category, filename string) error {
	return s.client.RemoveObject(ctx, s.bucket, category+"/"+filename, minio.RemoveObjectOptions{})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func plural(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// sorted keeps the missing-variable message stable between restarts, because a
// message that reorders itself reads like a different problem.
func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
