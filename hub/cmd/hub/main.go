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
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
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

// activeModules resolves AVURUOPS_MODULES (empty = all). A typo must fail the
// deploy loudly — silently skipping a module's schema is worse than a crash
// loop with a clear message.
func activeModules() (modules.Set, error) {
	set, err := modules.Parse(os.Getenv("AVURUOPS_MODULES"))
	if err != nil {
		return nil, fmt.Errorf("AVURUOPS_MODULES: %w", err)
	}
	return set, nil
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

	active, err := activeModules()
	if err != nil {
		return err
	}
	if err := store.Migrate(ctx, active); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	retention := ch.Retention{
		TracesDays:   envIntOr("AVURUOPS_RETENTION_TRACES_DAYS", 7),
		LogsDays:     envIntOr("AVURUOPS_RETENTION_LOGS_DAYS", 3),
		MetricsDays:  envIntOr("AVURUOPS_RETENTION_METRICS_DAYS", 7),
		ProfilesDays: envIntOr("AVURUOPS_RETENTION_PROFILES_DAYS", 3),
		// Errors default higher: low volume after fingerprint grouping, and
		// issue history is the point of the module.
		ErrorsDays: envIntOr("AVURUOPS_RETENTION_ERRORS_DAYS", 30),
		// Alert history is tiny; keep a month for the UI timeline.
		AlertsDays: envIntOr("AVURUOPS_RETENTION_ALERTS_DAYS", 30),
	}
	// Don't touch a disabled module's TTL: its tables were never created —
	// or, if the module was enabled once, they still hold data we no longer
	// manage. Either way the ALTER is not ours to run.
	if !active.Enabled(modules.Logs) {
		retention.LogsDays = 0
	}
	if !active.Enabled(modules.InfraMetrics) {
		retention.MetricsDays = 0
	}
	if !active.Enabled(modules.Profiling) {
		retention.ProfilesDays = 0
	}
	if !active.Enabled(modules.ErrorTracking) {
		retention.ErrorsDays = 0
	}
	if err := store.ApplyRetention(ctx, retention); err != nil {
		return fmt.Errorf("applying retention: %w", err)
	}
	slog.Info("migration complete",
		"modules", active.Names(),
		"tracesRetentionDays", retention.TracesDays,
		"logsRetentionDays", retention.LogsDays,
		"metricsRetentionDays", retention.MetricsDays,
		"profilesRetentionDays", retention.ProfilesDays,
		"errorsRetentionDays", retention.ErrorsDays)
	return nil
}

func run() error {
	addr := envOr("AVURUOPS_LISTEN_ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	provider := connectStore(ctx, clickhouseConfig())

	authSvc, anonID := authService(provider)
	if authSvc != nil {
		go bootstrapAdmin(ctx, authSvc, provider)
	}

	active, err := activeModules()
	if err != nil {
		return err
	}
	slog.Info("active modules", "modules", active.Names())

	groupsConfig, err := loadGroupsConfig(ctx)
	if err != nil {
		return err
	}
	alertsConfig, err := loadAlertingConfig(ctx)
	if err != nil {
		return err
	}
	greenConfig, err := loadGreenConfig(ctx)
	if err != nil {
		return err
	}
	// OIDC is hot-reloaded like the other mounted configs; nil accessors when
	// AVURUOPS_AUTH_OIDC_CONFIG is unset (or auth is disabled). Installs the
	// SSO group→grant mapper on authSvc as a side effect.
	oidcProvider, oidcSettings, err := loadOIDCConfig(ctx, authSvc)
	if err != nil {
		return err
	}

	// One notifier shared by the evaluator and the channels test endpoint, so
	// the SSRF policy is identical on both paths.
	var notifier alerting.Notifier
	if active.Enabled(modules.Alerting) {
		notifier = alerting.NewWebhookNotifier(5*time.Second, 3, webhookAllowCIDRs())
	}

	// Hub is API-only: the UI is a separate deployable (its own nginx pod),
	// reached single-origin via the gateway/ingress. See agent_docs/architecture.md.
	mux := http.NewServeMux()
	api.Register(mux, provider, api.Config{
		RetentionTracesDays:   envIntOr("AVURUOPS_RETENTION_TRACES_DAYS", 7),
		RetentionLogsDays:     envIntOr("AVURUOPS_RETENTION_LOGS_DAYS", 3),
		RetentionMetricsDays:  envIntOr("AVURUOPS_RETENTION_METRICS_DAYS", 7),
		RetentionProfilesDays: envIntOr("AVURUOPS_RETENTION_PROFILES_DAYS", 3),
		Projects:              splitCSV(envOr("AVURUOPS_PROJECTS", "")),
		Modules:               active,
		GroupsConfig:          groupsConfig,
		AlertsConfig:          alertsConfig,
		Notifier:              notifier,
		Auth:                  authSvc,
		AnonymousIdentity:     anonID,
		GreenConfig:           greenConfig,
		OIDC:                  oidcProvider,
		OIDCSettings:          oidcSettings,
	})

	// The alerting evaluator is a single background loop (see runAlertingEvaluator);
	// started only when the module is active.
	if active.Enabled(modules.Alerting) {
		go runAlertingEvaluator(ctx, provider, groupsConfig, alertsConfig, greenConfig, notifier, splitCSV(envOr("AVURUOPS_PROJECTS", "")), active)
	}

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

// authService builds the auth stack from env. AVURUOPS_AUTH_ENABLED=false
// restores the fully-open pre-auth behavior (labs, demos). A misconfigured
// kill-switch must fail closed: only the exact value "false" disables auth;
// anything unrecognized (a typo, say) leaves auth ENABLED and logs an error,
// rather than silently opening every request up as anonymous admin.
// AVURUOPS_AUTH_ANONYMOUS_ROLE + _PROJECTS enable the project-scoped
// anonymous identity (the docs-demo mode from the AEP).
func authService(provider api.StoreProvider) (*auth.Service, *auth.Identity) {
	v := envOr("AVURUOPS_AUTH_ENABLED", "true")
	switch v {
	case "false":
		slog.Warn("authentication is DISABLED — every request is anonymous admin")
		return nil, nil
	case "true":
	default:
		slog.Error("unrecognized AVURUOPS_AUTH_ENABLED value, auth remains enabled", "value", v)
	}
	ttl := time.Duration(envIntOr("AVURUOPS_AUTH_SESSION_TTL_HOURS", 168)) * time.Hour
	svc := auth.NewService(provider, ttl)

	var anon *auth.Identity
	role := os.Getenv("AVURUOPS_AUTH_ANONYMOUS_ROLE")
	projectsRaw := os.Getenv("AVURUOPS_AUTH_ANONYMOUS_PROJECTS")
	if role != "" {
		r, ok := auth.ParseRole(role)
		if !ok {
			slog.Error("invalid AVURUOPS_AUTH_ANONYMOUS_ROLE, ignoring anonymous mode", "role", role)
		} else {
			projects := splitCSV(projectsRaw)
			if len(projects) == 0 {
				slog.Error("AVURUOPS_AUTH_ANONYMOUS_PROJECTS is required with anonymous mode (never implicit '*'), ignoring")
			} else {
				// Both cheap insurance against a fat-fingered config: neither
				// is refused, both are visible operator choices, but a
				// wildcard scope or an above-Viewer anonymous role is worth
				// a second look in the logs.
				for _, p := range projects {
					if p == "*" {
						slog.Warn("AVURUOPS_AUTH_ANONYMOUS_PROJECTS contains '*' — anonymous access applies to every project", "role", r)
						break
					}
				}
				if r != auth.RoleViewer {
					slog.Warn("AVURUOPS_AUTH_ANONYMOUS_ROLE is stronger than viewer", "role", r)
				}
				anon = &auth.Identity{Name: "Anonymous", Anonymous: true}
				for _, p := range projects {
					anon.Grants = append(anon.Grants, auth.Grant{Scope: p, Role: r})
				}
				slog.Info("anonymous access enabled", "role", r, "projects", projects)
			}
		}
	} else if projectsRaw != "" {
		// _PROJECTS without _ROLE was a silent no-op before; a bare-typo'd
		// env var deserves a log line, not silence.
		slog.Error("AVURUOPS_AUTH_ANONYMOUS_PROJECTS is set but AVURUOPS_AUTH_ANONYMOUS_ROLE is empty, ignoring anonymous mode")
	}
	return svc, anon
}

// bootstrapAdmin waits for the store, then ensures the admin user exists.
// AVURUOPS_AUTH_ADMIN_PASSWORD empty → generate one and log it ONCE, only on
// the attempt whose Bootstrap call actually created the admin (created==true
// tells us that directly, so there's no separate pre-check to race against).
// Helm always sets the password; the generated path is the bare-compose
// convenience.
//
// svc.Bootstrap can fail transiently — e.g. ClickHouse is reachable but the
// auth tables haven't been migrated yet (migrate runs as a separate
// job/hook) — so this retries forever rather than giving up (connectStore's
// degraded-not-crashed philosophy): every 5s for the first 2 minutes, then
// every 60s. A one-time Error when crossing the 2-minute mark flags a likely
// deploy bug; retries continue quietly (Warn per failure only) after that.
func bootstrapAdmin(ctx context.Context, svc *auth.Service, provider api.StoreProvider) {
	password := os.Getenv("AVURUOPS_AUTH_ADMIN_PASSWORD")
	generated := password == ""
	if generated {
		password = auth.NewID()
	}

	for provider() == nil {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}

	start := time.Now()
	erroredOnce := false
	for {
		created, err := svc.Bootstrap(ctx, password)
		if err == nil {
			if generated && created {
				// Printed once, only on the attempt that actually created the admin.
				slog.Warn("generated admin password — change it after first login",
					"email", "admin", "password", password)
			}
			return
		}
		slog.Warn("admin bootstrap failed, retrying", "error", err)
		elapsed := time.Since(start)
		if !erroredOnce && elapsed >= 2*time.Minute {
			slog.Error("admin bootstrap still failing after 2 minutes — check ClickHouse/migration state", "error", err)
			erroredOnce = true
		}
		interval := 5 * time.Second
		if elapsed >= 2*time.Minute {
			interval = 60 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
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

// splitCSV parses a comma-separated env value, trimming blanks (used for
// AVURUOPS_PROJECTS, the config-defined project list).
func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
