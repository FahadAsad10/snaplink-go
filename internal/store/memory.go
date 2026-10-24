package store

import (
	"context"
	"sync"
	"time"
)

type Memory struct {
	mu    sync.RWMutex
	links map[string]Link
	now   func() time.Time
}

func NewMemory() *Memory {
	return &Memory{links: map[string]Link{}, now: time.Now}
}

func (m *Memory) Create(_ context.Context, link Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.links[link.Code]; ok && !m.expired(existing) {
		return ErrExists
	}
	m.links[link.Code] = link
	return nil
}

func (m *Memory) Get(_ context.Context, code string) (Link, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	link, ok := m.links[code]
	if !ok || m.expired(link) {
		return Link{}, ErrNotFound
	}
	return link, nil
}

func (m *Memory) Hit(_ context.Context, code string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	link, ok := m.links[code]
	if !ok || m.expired(link) {
		return "", ErrNotFound
	}
	link.Clicks++
	m.links[code] = link
	return link.URL, nil
}

func (m *Memory) Delete(_ context.Context, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[code]; !ok {
		return ErrNotFound
	}
	delete(m.links, code)
	return nil
}

func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) expired(l Link) bool {
	return l.ExpiresAt != nil && !m.now().Before(*l.ExpiresAt)
}
