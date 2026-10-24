package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Both implementations must pass the same contract.
func implementations(t *testing.T) map[string]func() (Store, func(time.Duration)) {
	return map[string]func() (Store, func(time.Duration)){
		"memory": func() (Store, func(time.Duration)) {
			m := NewMemory()
			now := time.Now()
			m.now = func() time.Time { return now }
			return m, func(d time.Duration) { now = now.Add(d) }
		},
		"redis": func() (Store, func(time.Duration)) {
			mr := miniredis.RunT(t)
			return NewRedis(redis.NewClient(&redis.Options{Addr: mr.Addr()})), mr.FastForward
		},
	}
}

func TestStoreContract(t *testing.T) {
	ctx := context.Background()
	for name, mk := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			s, _ := mk()
			link := Link{Code: "abc", URL: "https://go.dev", CreatedAt: time.Now().UTC()}

			if err := s.Create(ctx, link); err != nil {
				t.Fatal(err)
			}
			if err := s.Create(ctx, link); !errors.Is(err, ErrExists) {
				t.Fatalf("duplicate create: got %v", err)
			}
			for range 3 {
				if url, err := s.Hit(ctx, "abc"); err != nil || url != link.URL {
					t.Fatalf("hit: %q %v", url, err)
				}
			}
			got, err := s.Get(ctx, "abc")
			if err != nil || got.Clicks != 3 || got.URL != link.URL {
				t.Fatalf("get: %+v %v", got, err)
			}
			if _, err := s.Hit(ctx, "missing"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("hit missing: %v", err)
			}
			if _, err := s.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("get missing: %v", err)
			}
			if err := s.Delete(ctx, "abc"); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, "abc"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("double delete: %v", err)
			}
			if err := s.Ping(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStoreExpiry(t *testing.T) {
	ctx := context.Background()
	for name, mk := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			s, advance := mk()
			exp := time.Now().Add(time.Hour)
			if err := s.Create(ctx, Link{Code: "tmp", URL: "https://x.dev", CreatedAt: time.Now(), ExpiresAt: &exp}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Hit(ctx, "tmp"); err != nil {
				t.Fatalf("before expiry: %v", err)
			}
			advance(2 * time.Hour)
			if _, err := s.Hit(ctx, "tmp"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("after expiry: %v", err)
			}
			// An expired code can be claimed again.
			if err := s.Create(ctx, Link{Code: "tmp", URL: "https://y.dev", CreatedAt: time.Now()}); err != nil {
				t.Fatalf("reuse: %v", err)
			}
		})
	}
}

func TestConcurrentHitsAreCounted(t *testing.T) {
	ctx := context.Background()
	for name, mk := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			s, _ := mk()
			_ = s.Create(ctx, Link{Code: "hot", URL: "https://x.dev", CreatedAt: time.Now()})
			var wg sync.WaitGroup
			for range 50 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = s.Hit(ctx, "hot")
				}()
			}
			wg.Wait()
			if got, _ := s.Get(ctx, "hot"); got.Clicks != 50 {
				t.Fatalf("clicks = %d, want 50", got.Clicks)
			}
		})
	}
}
