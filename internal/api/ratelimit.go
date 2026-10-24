package api

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipLimiter gives each client IP its own token bucket and forgets idle clients.
type ipLimiter struct {
	mu      sync.Mutex
	clients map[string]*client
	limit   rate.Limit
	burst   int
}

type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newIPLimiter(perMinute, burst int) *ipLimiter {
	return &ipLimiter{
		clients: map[string]*client{},
		limit:   rate.Limit(float64(perMinute) / 60),
		burst:   burst,
	}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.clients[ip]
	if !ok {
		c = &client{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.clients[ip] = c
	}
	c.lastSeen = time.Now()
	return c.limiter.Allow()
}

func (l *ipLimiter) cleanup(idle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, c := range l.clients {
		if time.Since(c.lastSeen) > idle {
			delete(l.clients, ip)
		}
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *ipLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded, try again shortly")
			return
		}
		next.ServeHTTP(w, r)
	})
}
