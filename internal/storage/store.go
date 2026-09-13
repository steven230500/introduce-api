// Package storage keeps the bytes a church uploads.
//
// There are two places those bytes can live. The droplet's own disk is fine
// for one church and a handful of images, and it is what a self-hosted
// installation gets for free. It is the wrong place for video: the machine is
// sized to run an API, its disk is small, and every byte served competes with
// the request that somebody's service is waiting on. Object storage is sized
// for exactly that and costs cents.
//
// Both are behind [Store] so the rest of the API never learns which one it
// got.
package storage

import "context"

type Store interface {
	// Save writes the bytes and returns the URL they can be read from.
	Save(ctx context.Context, category, filename string, data []byte, contentType string) (string, error)

	// Delete removes them. A file that is already gone is not an error: the
	// library row is what the operator sees, and it has been removed either
	// way.
	Delete(ctx context.Context, category, filename string) error

	// Kind names the backend for the health endpoint and the logs, so a deploy
	// that silently fell back to disk is visible.
	Kind() string
}
