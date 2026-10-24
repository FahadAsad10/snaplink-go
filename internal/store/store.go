// Package store persists short links. Redis is used in production and an
// in-memory implementation backs tests and local development.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("link not found")
	ErrExists   = errors.New("code already in use")
)

type Link struct {
	Code      string     `json:"code"`
	URL       string     `json:"url"`
	Clicks    int64      `json:"clicks"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Store interface {
	// Create saves a new link and fails with ErrExists if the code is taken.
	Create(ctx context.Context, link Link) error
	Get(ctx context.Context, code string) (Link, error)
	// Hit atomically increments the click counter and returns the target URL.
	Hit(ctx context.Context, code string) (string, error)
	Delete(ctx context.Context, code string) error
	Ping(ctx context.Context) error
}
