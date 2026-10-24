// Package api exposes the HTTP interface: a JSON API for links, the redirect
// endpoint, health checks, Prometheus metrics and a small web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/FahadAsad10/snaplink-go/internal/shortid"
	"github.com/FahadAsad10/snaplink-go/internal/store"
)

type Config struct {
	BaseURL        string // public origin used to build short URLs, e.g. https://snap.link
	CodeLength     int
	RatePerMinute  int // link creations allowed per IP per minute
	RateBurst      int
	MaxURLLength   int
	MaxTTL         time.Duration
	Registry       *prometheus.Registry
	Logger         *slog.Logger
	StaticFS       fs.FS
	DisableLimiter bool
}

type Server struct {
	cfg     Config
	store   store.Store
	metrics *metrics
	limiter *ipLimiter
	log     *slog.Logger
	mux     *http.ServeMux
}

func New(s store.Store, cfg Config) *Server {
	if cfg.CodeLength == 0 {
		cfg.CodeLength = 7
	}
	if cfg.RatePerMinute == 0 {
		cfg.RatePerMinute = 30
	}
	if cfg.RateBurst == 0 {
		cfg.RateBurst = 10
	}
	if cfg.MaxURLLength == 0 {
		cfg.MaxURLLength = 2048
	}
	if cfg.MaxTTL == 0 {
		cfg.MaxTTL = 365 * 24 * time.Hour
	}
	if cfg.Registry == nil {
		cfg.Registry = prometheus.NewRegistry()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	srv := &Server{
		cfg:     cfg,
		store:   s,
		metrics: newMetrics(cfg.Registry),
		limiter: newIPLimiter(cfg.RatePerMinute, cfg.RateBurst),
		log:     cfg.Logger,
		mux:     http.NewServeMux(),
	}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	create := http.Handler(http.HandlerFunc(s.handleCreate))
	if !s.cfg.DisableLimiter {
		create = s.limiter.middleware(create)
	}
	s.handle("POST /api/links", create)
	s.handle("GET /api/links/{code}", http.HandlerFunc(s.handleGet))
	s.handle("DELETE /api/links/{code}", http.HandlerFunc(s.handleDelete))
	s.handle("GET /healthz", http.HandlerFunc(s.handleHealth))
	s.mux.Handle("GET /metrics", promhttp.HandlerFor(s.cfg.Registry, promhttp.HandlerOpts{}))
	s.handle("GET /{code}", http.HandlerFunc(s.handleRedirect))
	if s.cfg.StaticFS != nil {
		s.mux.Handle("GET /{$}", http.FileServerFS(s.cfg.StaticFS))
	}
}

// handle registers a route wrapped with metrics and access logging.
func (s *Server) handle(pattern string, h http.Handler) {
	route := pattern
	s.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		elapsed := time.Since(start)
		s.metrics.requests.WithLabelValues(route, strconv.Itoa(rec.status)).Inc()
		s.metrics.latency.WithLabelValues(route).Observe(elapsed.Seconds())
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", elapsed.Milliseconds())
	}))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// StartJanitor periodically forgets rate-limit state for idle clients.
func (s *Server) StartJanitor(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	go func() {
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.limiter.cleanup(10 * time.Minute)
			}
		}
	}()
}

type createRequest struct {
	URL        string `json:"url"`
	CustomCode string `json:"custom_code,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

type linkResponse struct {
	store.Link
	ShortURL string `json:"short_url"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := s.validateURL(req.URL); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if req.TTLSeconds < 0 || time.Duration(req.TTLSeconds)*time.Second > s.cfg.MaxTTL {
		writeError(w, http.StatusUnprocessableEntity, "ttl_seconds is out of range")
		return
	}

	link := store.Link{URL: req.URL, CreatedAt: time.Now().UTC()}
	if req.TTLSeconds > 0 {
		exp := link.CreatedAt.Add(time.Duration(req.TTLSeconds) * time.Second)
		link.ExpiresAt = &exp
	}

	if req.CustomCode != "" {
		if !shortid.Valid(req.CustomCode) || reserved[strings.ToLower(req.CustomCode)] {
			writeError(w, http.StatusUnprocessableEntity, "custom_code must be 3-32 letters, digits, - or _ and not a reserved word")
			return
		}
		link.Code = req.CustomCode
		if err := s.store.Create(r.Context(), link); err != nil {
			s.storeError(w, err)
			return
		}
	} else if err := s.createWithRandomCode(r.Context(), &link); err != nil {
		s.storeError(w, err)
		return
	}

	s.metrics.created.Inc()
	w.Header().Set("Location", "/api/links/"+link.Code)
	writeJSON(w, http.StatusCreated, s.response(link))
}

// createWithRandomCode retries on the rare collision instead of checking first,
// which would race with concurrent requests.
func (s *Server) createWithRandomCode(ctx context.Context, link *store.Link) error {
	for range 5 {
		code, err := shortid.New(s.cfg.CodeLength)
		if err != nil {
			return err
		}
		link.Code = code
		err = s.store.Create(ctx, *link)
		if !errors.Is(err, store.ErrExists) {
			return err
		}
	}
	return errors.New("could not allocate a unique code")
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	link, err := s.store.Get(r.Context(), r.PathValue("code"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.response(link))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.Context(), r.PathValue("code")); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	target, err := s.store.Hit(r.Context(), r.PathValue("code"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.metrics.redirects.Inc()
	w.Header().Set("Cache-Control", "no-store") // keep click counts accurate
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "store": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) validateURL(raw string) string {
	if raw == "" {
		return "url is required"
	}
	if len(raw) > s.cfg.MaxURLLength {
		return "url is too long"
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "url must be an absolute http or https URL"
	}
	if base, err := url.Parse(s.cfg.BaseURL); err == nil && base.Host != "" && strings.EqualFold(u.Host, base.Host) {
		return "cannot shorten a link that points back to this service"
	}
	return ""
}

func (s *Server) response(l store.Link) linkResponse {
	return linkResponse{Link: l, ShortURL: strings.TrimRight(s.cfg.BaseURL, "/") + "/" + l.Code}
}

func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "link not found")
	case errors.Is(err, store.ErrExists):
		writeError(w, http.StatusConflict, "that code is already taken")
	default:
		s.log.Error("store error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// Paths that must never be claimed as short codes.
var reserved = map[string]bool{"api": true, "healthz": true, "metrics": true, "static": true, "admin": true}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
