// Package store is the engine's persistence interface. Game code talks to a
// Store, never to SQL, so the SQLite backend can later be joined by Postgres.
package store

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is implemented by every storage backend.
type Store interface {
	// Ping reports whether the backend can serve requests.
	Ping(ctx context.Context) error
	// Setting returns the value for key, or ErrNotFound.
	Setting(ctx context.Context, key string) (string, error)
	// SetSetting creates or replaces the value for key.
	SetSetting(ctx context.Context, key, value string) error
	// Close releases the backend's resources.
	Close() error
}
