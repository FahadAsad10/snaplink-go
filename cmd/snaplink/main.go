package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/redis/go-redis/v9"

	"github.com/FahadAsad10/snaplink-go/internal/api"
	"github.com/FahadAsad10/snaplink-go/internal/store"
	"github.com/FahadAsad10/snaplink-go/web"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	addr := env("ADDR", ":8080")
	baseURL := env("BASE_URL", "http://localhost:8080")

	var st store.Store
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			return err
		}
		rdb := redis.NewClient(opts)
		defer rdb.Close()
		st = store.NewRedis(rdb)
		log.Info("using redis store", "addr", opts.Addr)
	} else {
		st = store.NewMemory()
		log.Warn("REDIS_URL not set, using in-memory store (data is lost on restart)")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	srv := api.New(st, api.Config{
		BaseURL:       baseURL,
		RatePerMinute: envInt("RATE_PER_MINUTE", 30),
		RateBurst:     envInt("RATE_BURST", 10),
		Registry:      reg,
		Logger:        log,
		StaticFS:      web.FS,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.StartJanitor(ctx)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr, "base_url", baseURL)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Let in-flight requests finish (Kubernetes sends SIGTERM before killing the pod).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}
