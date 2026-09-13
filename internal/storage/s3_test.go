package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestS3NotConfiguredMeansDisk(t *testing.T) {
	// A deployment with no bucket is a self-hosted one, and must keep working
	// with no extra settings at all.
	for _, key := range []string{"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_PUBLIC_BASE"} {
		t.Setenv(key, "")
	}
	if S3FromEnv().Configured() {
		t.Fatal("an empty environment reads as a configured bucket")
	}
}

func TestHalfConfiguredBucketNamesEverythingMissing(t *testing.T) {
	// One restart per missing variable is how a Sunday morning deploy goes
	// wrong four times in a row.
	cfg := S3Config{Endpoint: "nyc3.digitaloceanspaces.com", Bucket: "introduce"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a bucket with no credentials validated")
	}
	for _, want := range []string{"S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_PUBLIC_BASE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %s", want, err)
		}
	}
}

func TestDiskStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewDisk(dir, "http://localhost:8080/files")
	if err != nil {
		t.Fatal(err)
	}

	url, err := store.Save(context.Background(), "images", "a.png", []byte("bytes"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://localhost:8080/files/images/a.png" {
		t.Fatalf("url is %q", url)
	}

	written, err := os.ReadFile(filepath.Join(dir, "images", "a.png"))
	if err != nil || string(written) != "bytes" {
		t.Fatalf("file on disk is %q, %v", written, err)
	}
}

func TestDeletingAFileThatIsAlreadyGoneIsFine(t *testing.T) {
	// The library row is what the operator sees, and it has been removed
	// either way. Failing here would leave a row they cannot get rid of.
	store, err := NewDisk(t.TempDir(), "http://x/files")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(context.Background(), "images", "never-existed.png"); err != nil {
		t.Fatalf("delete of a missing file returned %v", err)
	}
}
