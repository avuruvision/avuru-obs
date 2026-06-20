package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	ch "github.com/avuru/avuru-obs/hub/internal/storage/clickhouse"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// `hub migrate` runs the schema migrator and exits (Helm pre-install/
	// pre-upgrade hook in k8s; same path in compose/dev).
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := runMigrate(); err != nil {
			slog.Error("migrate failed", "error", err)
			os.Exit(1)
		}
		return
	}

	healthcheck := flag.Bool("healthcheck", false, "probe the local hub and exit (for container healthchecks)")
	flag.Parse()
	if *healthcheck {
		os.Exit(probe())
	}
	if err := run(); err != nil {
		slog.Error("hub exited", "error", err)
		os.Exit(1)
	}
}

// probe hits the local /healthz; the hub image is distroless so the binary is
// its own healthcheck tool.
func probe() int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:8080/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func clickhouseConfig() ch.Config {
	return ch.Config{
		Addr:     envOr("AVURUOPS_CLICKHOUSE_ADDR", "localhost:9000"),
		Database: envOr("AVURUOPS_CLICKHOUSE_DATABASE", "otel"),
		Username: envOr("AVURUOPS_CLICKHOUSE_USER", "avuru"),
		Password: envOr("AVURUOPS_CLICKHOUSE_PASSWORD", "avuru"),
	}
}

// runMigrate applies schema migrations + retention, then exits. Retries
// ClickHouse for ~60s so it tolerates the database still coming up.
func runMigrate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := clickhouseConfig()
	var store *ch.Store
	deadline := time.Now().Add(60 * time.Second)
	for {
		s, err := ch.New(ctx, cfg)
		if err == nil {
			store = s
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("clickhouse not reachable at %s: %w", cfg.Addr, err)
		}
		slog.Warn("clickhouse not ready, retrying", "addr", cfg.Addr, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	defer func() { _ = store.Close() }()

	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	tracesDays := envIntOr("AVURUOPS_RETENTION_TRACES_DAYS", 7)
	logsDays := envIntOr("AVURUOPS_RETENTION_LOGS_DAYS", 3)
	if err := store.ApplyRetention(ctx, tracesDays, logsDays); err != nil {
		return fmt.Errorf("applying retention: %w", err)
	}
	slog.Info("migration complete", "tracesRetentionDays", tracesDays, "logsRetentionDays", logsDays)
	return nil
}

func run() error {
	addr := envOr("AVURUOPS_LISTEN_ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	provider := connectStore(ctx, clickhouseConfig())

	// Hub is API-only: the UI is a separate deployable (its own nginx pod),
	// reached single-origin via the gateway/ingress. See agent_docs/architecture.md.
	mux := http.NewServeMux()
	api.Register(mux, provider)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("hub listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving: %w", err)
		}
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}

// connectStore keeps trying ClickHouse in the background; the API serves 503
// on signal endpoints until the store is up (and again if it never comes up).
// The hub itself must start regardless — a ClickHouse outage is degraded
// service, not a crash loop.
func connectStore(ctx context.Context, cfg ch.Config) api.StoreProvider {
	var current atomic.Pointer[ch.Store]
	go func() {
		for {
			store, err := ch.New(ctx, cfg)
			if err == nil {
				current.Store(store)
				slog.Info("clickhouse connected", "addr", cfg.Addr)
				return
			}
			slog.Warn("clickhouse unavailable, retrying in 5s", "addr", cfg.Addr, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	return func() storage.Store {
		if s := current.Load(); s != nil {
			return s
		}
		return nil
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		slog.Warn("invalid int env, using default", "key", key, "value", v, "default", def)
	}
	return def
}
