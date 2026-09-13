package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// DiskStore keeps uploads on the machine running the API.
//
// Right for a self-hosted install and for local development. Wrong for a
// hosted plan with video in it, which is what [S3Store] is for.
type DiskStore struct {
	baseDir string
	baseURL string
}

func NewDisk(baseDir, baseURL string) (*DiskStore, error) {
	for _, sub := range []string{"images", "audio", "videos"} {
		if err := os.MkdirAll(filepath.Join(baseDir, sub), 0755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", sub, err)
		}
	}
	return &DiskStore{baseDir: baseDir, baseURL: baseURL}, nil
}

func (s *DiskStore) Kind() string { return "disk" }

func (s *DiskStore) Save(_ context.Context, category, filename string, data []byte, _ string) (string, error) {
	dir := filepath.Join(s.baseDir, category)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s/%s", s.baseURL, category, filename), nil
}

func (s *DiskStore) Delete(_ context.Context, category, filename string) error {
	err := os.Remove(filepath.Join(s.baseDir, category, filename))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
