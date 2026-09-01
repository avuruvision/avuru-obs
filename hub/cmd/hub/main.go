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
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/checks"
	"github.com/avuru/avuru-obs/hub/internal/collection"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	ch "github.com/avuru/avuru-obs/hub/internal/storage/clickhouse"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// `hub migrate` runs the schema migrator and exits (Helm post-install/
	// post-upgrade hook in k8s; same path in compose/dev). The serving hub also
	// applies missing migrations itself — see schema.go for why that is not
	// redundant.
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
		Addr:     envOr("AVURUOBS_CLICKHOUSE_ADDR", "localhost:9000"),
		Database: envOr("AVURUOBS_CLICKHOUSE_DATABASE", "otel"),
		Username: envOr("AVURUOBS_CLICKHOUSE_USER", "avuru"),
		Password: envOr("AVURUOBS_CLICKHOUSE_PASSWORD", "avuru"),
	}
}

// storageConnection is the non-secret half of clickhouseConfig, for the
// admin-only Storage view. Built by naming the three fields rather than
// copying the struct, so a credential added to ch.Config later cannot ride
// along into an HTTP response by accident.
func storageConnection() api.StorageConnection {
	cfg := clickhouseConfig()
	return api.StorageConnection{
		Address:  cfg.Addr,
		Database: cfg.Database,
		Username: cfg.Username,
	}
}

// databaseNamePattern is the ClickHouse identifier shape we accept.
var databaseNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateDatabaseName guards the one env value that is interpolated straight
// into DDL (the migrations name their database through a placeholder, so it
// cannot be a bound parameter). Rejecting at boot keeps a malformed name from
// becoming a confusing mid-migration syntax error — or worse.
func validateDatabaseName(db string) error {
	if !databaseNamePattern.MatchString(db) {
		return fmt.Errorf("AVURUOBS_CLICKHOUSE_DATABASE: %q is not a valid identifier (letters, digits and underscore, not starting with a digit)", db)
	}
	return nil
}

// activeModules resolves AVURUOBS_MODULES (empty = all). A typo must fail the
// deploy loudly — silently skipping a module's schema is worse than a crash
// loop with a clear message.
func activeModules() (modules.Set, error) {
	set, err := modules.Parse(os.Getenv("AVURUOBS_MODULES"))
	if err != nil {
		return nil, fmt.Errorf("AVURUOBS_MODULES: %w", err)
	}
	return set, nil
}

// runMigrate applies schema migrations + retention, then exits. Retries
// ClickHouse for ~60s so it tolerates the database still coming up.
func runMigrate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := clickhouseConfig()
	if err := validateDatabaseName(cfg.Database); err != nil {
		return err
	}
	var store *ch.Store
	// Configurable because the wait is a race against ClickHouse's own startup:
	// a cold cluster restoring a large PVC can take longer than a minute, and
	// the Job failing there is what leaves an install unmigrated.
	deadline := time.Now().Add(time.Duration(envIntOr("AVURUOBS_MIGRATE_WAIT_SECONDS", 60)) * time.Second)
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
		TracesDays:   envIntOr("AVURUOBS_RETENTION_TRACES_DAYS", 7),
		LogsDays:     envIntOr("AVURUOBS_RETENTION_LOGS_DAYS", 3),
		MetricsDays:  envIntOr("AVURUOBS_RETENTION_METRICS_DAYS", 7),
		ProfilesDays: envIntOr("AVURUOBS_RETENTION_PROFILES_DAYS", 3),
		// Errors default higher: low volume after fingerprint grouping, and
		// issue history is the point of the module.
		ErrorsDays: envIntOr("AVURUOBS_RETENTION_ERRORS_DAYS", 30),
		// Alert history is tiny; keep a month for the UI timeline.
		AlertsDays: envIntOr("AVURUOBS_RETENTION_ALERTS_DAYS", 30),
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
	addr := envOr("AVURUOBS_LISTEN_ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	active, err := activeModules()
	if err != nil {
		return err
	}
	slog.Info("active modules", "modules", active.Names())

	chCfg := clickhouseConfig()
	if err := validateDatabaseName(chCfg.Database); err != nil {
		return err
	}
	// The gate answers "is the schema there?" for everything that needs tables,
	// and heals it when the chart's migrate hook never ran. Built before the
	// store so the subsystems below can wait on it immediately.
	gate := newSchemaGate(active, chCfg.Database, schemaAutoMigrate())
	provider := connectStore(ctx, chCfg, gate)

	authSvc, anonID := authService(provider)
	// Demo mode: resolve the settings once so the SAME generated password feeds
	// both the demo-user bootstrap and the /auth/demo handler (it never leaves
	// the process). Requires auth to be on.
	demoEnabled := envOr("AVURUOBS_DEMO_ENABLED", "false") == "true" && authSvc != nil
	demoEmail := envOr("AVURUOBS_DEMO_EMAIL", "demo@avuru.obs")
	demoPassword := os.Getenv("AVURUOBS_DEMO_PASSWORD")
	// Runtime collection control (design/2026-07-27-collection-control-plane.md)
	// — off unless the install opts in, since it exposes a write API that
	// reconfigures the sensors.
	collectionRuntimeControlEnabled := envOr("AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED", "false") == "true"
	if demoEnabled {
		if demoPassword == "" {
			demoPassword = auth.NewID() // held in-process; never disclosed
		}
		go ensureDemoUser(ctx, authSvc, gate, demoEmail, demoPassword)
	}
	if authSvc != nil {
		go bootstrapAdmin(ctx, authSvc, gate)
	}

	// Chart-provisioned ingest keys (the sensor's). Parsed eagerly so a
	// malformed seed fails the boot loudly instead of surfacing later as
	// "enforce mode silently dropped our telemetry".
	seedKeys, err := parseIngestSeedKeys(os.Getenv("AVURUOBS_INGEST_SEED_KEYS"))
	if err != nil {
		return err
	}
	go seedIngestKeys(ctx, provider, gate, seedKeys)

	groupsConfig, err := loadGroupsConfig(ctx)
	if err != nil {
		return err
	}
	// ONE resolver, handed to both the API and the alerting evaluator below.
	// The evaluator does not go through the API, so a second merge point would
	// mean a group authored in the UI shows as critical on /health and never
	// pages. Keep both call sites reading this variable
	// (design/2026-08-07-service-groups-crud.md).
	groupsResolver := health.NewResolver(groupsConfig, func() health.GroupStore {
		st := provider()
		if st == nil {
			return nil // resolver degrades to config-only
		}
		return st
	})
	alertsConfig, err := loadAlertingConfig(ctx)
	if err != nil {
		return err
	}
	greenConfig, err := loadGreenConfig(ctx)
	if err != nil {
		return err
	}
	aiConfig, err := loadAIConfig(ctx)
	if err != nil {
		return err
	}
	topologyConfig, err := loadTopologyConfig(ctx)
	if err != nil {
		return err
	}
	// OIDC is hot-reloaded like the other mounted configs; nil accessors when
	// AVURUOBS_AUTH_OIDC_CONFIG is unset (or auth is disabled). Installs the
	// SSO group→grant mapper on authSvc as a side effect. provider is threaded
	// through so the mapping cache can overlay UI-authored rules read from
	// ClickHouse — it may still be nil here (not yet connected); the cache
	// degrades to config-only until it is. oidcMapping is the same cache,
	// handed to api.Config below so the admin CRUD (oidc_mapping.go) reads and
	// refreshes the one mapping the hub is actually enforcing, not a second
	// copy of it.
	oidcProvider, oidcSettings, oidcMapping, err := loadOIDCConfig(ctx, authSvc, provider)
	if err != nil {
		return err
	}

	// One notifier shared by the evaluator and the channels test endpoint, so
	// the SSRF policy is identical on both paths.
	var notifier alerting.Notifier
	if active.Enabled(modules.Alerting) {
		notifier = alerting.NewWebhookNotifier(5*time.Second, 3, webhookAllowCIDRs())
	}

	// CSRF origin policy, resolved before the router so a bad mode fails the
	// boot instead of surfacing later as "every write 403s".
	originCheck, err := originCheckMode()
	if err != nil {
		return err
	}

	// Hub is API-only: the UI is a separate deployable (its own nginx pod),
	// reached single-origin via the gateway/ingress. See agent_docs/architecture.md.
	mux := http.NewServeMux()
	api.Register(mux, provider, api.Config{
		RetentionTracesDays:             envIntOr("AVURUOBS_RETENTION_TRACES_DAYS", 7),
		RetentionLogsDays:               envIntOr("AVURUOBS_RETENTION_LOGS_DAYS", 3),
		RetentionMetricsDays:            envIntOr("AVURUOBS_RETENTION_METRICS_DAYS", 7),
		RetentionProfilesDays:           envIntOr("AVURUOBS_RETENTION_PROFILES_DAYS", 3),
		Projects:                        splitCSV(envOr("AVURUOBS_PROJECTS", "")),
		Modules:                         active,
		Groups:                          groupsResolver,
		AlertsConfig:                    alertsConfig,
		SchemaStatus:                    gate.Status,
		Notifier:                        notifier,
		Auth:                            authSvc,
		AnonymousIdentity:               anonID,
		TrustedOrigins:                  trustedOrigins(),
		OriginCheck:                     originCheck,
		DemoEnabled:                     demoEnabled,
		DemoEmail:                       demoEmail,
		DemoPassword:                    demoPassword,
		GreenConfig:                     greenConfig,
		AIConfig:                        aiConfig,
		CostRates:                       costRates(),
		MeshScrapeJob:                   envOr("AVURUOBS_MESH_SCRAPE_JOB", ""),
		Topology:                        topologyConfig,
		OIDC:                            oidcProvider,
		OIDCSettings:                    oidcSettings,
		OIDCMapping:                     oidcMapping,
		IngestInternalToken:             envOr("AVURUOBS_INGEST_INTERNAL_TOKEN", ""),
		StorageConnection:               storageConnection(),
		CollectionRuntimeControlEnabled: collectionRuntimeControlEnabled,
		CollectionApplier:               collectionApplier(collectionRuntimeControlEnabled),
	})

	// Per-project retention: a background sweep, not a table TTL (see
	// retention.go). It runs on every install — it is inert until a project
	// asks for a shorter window than the global one.
	go runRetentionTrimmer(ctx, provider, gate, time.Duration(envIntOr("AVURUOBS_RETENTION_TRIM_INTERVAL_MINUTES", 60))*time.Minute)

	// The alerting evaluator is a single background loop (see runAlertingEvaluator);
	// started only when the module is active.
	if active.Enabled(modules.Alerting) {
		go runAlertingEvaluator(ctx, provider, gate, groupsResolver, alertsConfig, greenConfig, aiConfig, notifier, splitCSV(envOr("AVURUOBS_PROJECTS", "")), active)
	}

	// Endpoint checks: the one signal that cannot be derived from observed
	// traffic — what happens when there is NO traffic. Started only with the
	// service-health module (checks attach to its groups and move their status)
	// and only when at least one is declared, so an install that wants none
	// runs no goroutine, writes no rows and behaves exactly as before.
	//
	// The emitter makes the hub an OTLP client of the gateway so a probe's own
	// request appears in RED, on the map and in traces like any other client's;
	// nil when no gateway is reachable, and checks still run and record.
	if active.Enabled(modules.ServiceHealth) && len(groupsConfig().AllChecks()) > 0 {
		emitter := checks.NewOTLPEmitter(
			envOr("AVURUOBS_GATEWAY_OTLP_ENDPOINT", ""),
			envOr("AVURUOBS_INGEST_KEY", ""),
			envOr("AVURUOBS_CHECKS_SERVICE_NAME", "avuru-obs-checks"),
			slog.Default(),
		)
		// One check, one probe — the declaration is global config, not
		// per-project, so running it once per tenant would multiply real HTTP
		// requests against someone's endpoint by the number of projects. The
		// results land under the default tenant, where the config lives.
		go runEndpointChecks(ctx, provider, gate, groupsConfig, emitter, storage.DefaultTenant)
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
//
// Once connected it hands the store to the schema gate. Order matters: the
// store is published to `current` FIRST, so serving behavior is unchanged and
// the API never waits on a migration.
func connectStore(ctx context.Context, cfg ch.Config, gate *schemaGate) api.StoreProvider {
	var current atomic.Pointer[ch.Store]
	go func() {
		for {
			store, err := ch.New(ctx, cfg)
			if err == nil {
				current.Store(store)
				slog.Info("clickhouse connected", "addr", cfg.Addr)
				go gate.run(ctx, store)
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

// authService builds the auth stack from env. AVURUOBS_AUTH_ENABLED=false
// restores the fully-open pre-auth behavior (labs, demos). A misconfigured
// kill-switch must fail closed: only the exact value "false" disables auth;
// anything unrecognized (a typo, say) leaves auth ENABLED and logs an error,
// rather than silently opening every request up as anonymous admin.
// AVURUOBS_AUTH_ANONYMOUS_ROLE + _PROJECTS enable the project-scoped
// anonymous identity (the docs-demo mode from the AEP).
func authService(provider api.StoreProvider) (*auth.Service, *auth.Identity) {
	v := envOr("AVURUOBS_AUTH_ENABLED", "true")
	switch v {
	case "false":
		slog.Warn("authentication is DISABLED — every request is anonymous admin")
		return nil, nil
	case "true":
	default:
		slog.Error("unrecognized AVURUOBS_AUTH_ENABLED value, auth remains enabled", "value", v)
	}
	ttl := time.Duration(envIntOr("AVURUOBS_AUTH_SESSION_TTL_HOURS", 168)) * time.Hour
	svc := auth.NewService(provider, ttl)

	var anon *auth.Identity
	role := os.Getenv("AVURUOBS_AUTH_ANONYMOUS_ROLE")
	projectsRaw := os.Getenv("AVURUOBS_AUTH_ANONYMOUS_PROJECTS")
	if role != "" {
		r, ok := auth.ParseRole(role)
		if !ok {
			slog.Error("invalid AVURUOBS_AUTH_ANONYMOUS_ROLE, ignoring anonymous mode", "role", role)
		} else {
			projects := splitCSV(projectsRaw)
			if len(projects) == 0 {
				slog.Error("AVURUOBS_AUTH_ANONYMOUS_PROJECTS is required with anonymous mode (never implicit '*'), ignoring")
			} else {
				// Both cheap insurance against a fat-fingered config: neither
				// is refused, both are visible operator choices, but a
				// wildcard scope or an above-Viewer anonymous role is worth
				// a second look in the logs.
				for _, p := range projects {
					if p == "*" {
						slog.Warn("AVURUOBS_AUTH_ANONYMOUS_PROJECTS contains '*' — anonymous access applies to every project", "role", r)
						break
					}
				}
				if r != auth.RoleViewer {
					slog.Warn("AVURUOBS_AUTH_ANONYMOUS_ROLE is stronger than viewer", "role", r)
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
		slog.Error("AVURUOBS_AUTH_ANONYMOUS_PROJECTS is set but AVURUOBS_AUTH_ANONYMOUS_ROLE is empty, ignoring anonymous mode")
	}
	return svc, anon
}

// bootstrapAdmin waits for the schema, then ensures the admin user exists.
// AVURUOBS_AUTH_ADMIN_PASSWORD empty → generate one and log it ONCE, only on
// the attempt whose Bootstrap call actually created the admin (created==true
// tells us that directly, so there's no separate pre-check to race against).
// Helm always sets the password; the generated path is the bare-compose
// convenience.
//
// Waiting on the gate rather than merely on the store is what removes the
// "Unknown table expression identifier 'auth_user'" flood: an unmigrated
// database is the gate's problem to report and heal, not something to
// rediscover here every 5s. Bootstrap can still fail transiently afterwards, so
// the retry loop stays: every 5s for the first 2 minutes, then every 60s, with
// a one-time Error at the crossover.
func bootstrapAdmin(ctx context.Context, svc *auth.Service, gate *schemaGate) {
	password := os.Getenv("AVURUOBS_AUTH_ADMIN_PASSWORD")
	generated := password == ""
	if generated {
		password = auth.NewID()
	}

	if !gate.wait(ctx) {
		return
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

// ensureDemoUser waits for the schema, then idempotently ensures the demo
// viewer exists. Retries every 5s until it succeeds (same degraded-not-crashed
// philosophy as bootstrapAdmin), escalating to a one-time Error at 2 minutes so
// a stuck demo bootstrap is as visible as a stuck admin one.
func ensureDemoUser(ctx context.Context, svc *auth.Service, gate *schemaGate, email, password string) {
	if !gate.wait(ctx) {
		return
	}
	start := time.Now()
	erroredOnce := false
	for {
		err := svc.EnsureDemoUser(ctx, email, password)
		if err == nil {
			return
		}
		slog.Warn("demo user bootstrap failed, retrying", "error", err)
		if !erroredOnce && time.Since(start) >= 2*time.Minute {
			slog.Error("demo user bootstrap still failing after 2 minutes — check ClickHouse/migration state", "error", err)
			erroredOnce = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// schemaAutoMigrate reads AVURUOBS_SCHEMA_AUTOMIGRATE. Fail-safe like
// AVURUOBS_AUTH_ENABLED: only the exact string "false" opts out, so a typo
// leaves self-healing ON rather than silently disarming the thing that keeps an
// install serving. Operators turn it off when ClickHouse schema is owned
// elsewhere (a DBA-managed external cluster, a query-only hub user).
func schemaAutoMigrate() bool {
	v := envOr("AVURUOBS_SCHEMA_AUTOMIGRATE", "true")
	switch v {
	case "false":
		slog.Info("schema self-heal disabled — migrations must be applied by `hub migrate`")
		return false
	case "true":
	default:
		slog.Error("unrecognized AVURUOBS_SCHEMA_AUTOMIGRATE value, self-heal remains enabled", "value", v)
	}
	return true
}

// costRates reads the chart-declared prices for reserved capacity. Unset (the
// default) leaves every rate at zero, which the API reports as "not priced"
// rather than as free — there is no pricing service to ask, deliberately.
func costRates() api.CostRates {
	return api.CostRates{
		CPUCoreHour: envFloatOr("AVURUOBS_COST_CPU_CORE_HOUR", 0),
		MemGiBHour:  envFloatOr("AVURUOBS_COST_MEM_GIB_HOUR", 0),
		Currency:    envOr("AVURUOBS_COST_CURRENCY", ""),
	}
}

// envFloatOr reads a float env var, falling back on anything unparseable. A
// mistyped rate must not stop the hub booting: it reports as unpriced, which
// is visible on the screen, rather than as a crash loop nobody can read.
func envFloatOr(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		slog.Warn("ignoring unusable cost rate", "key", key, "value", v)
		return def
	}
	return f
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

// collectionApplier picks the real cluster applier when runtime collection
// control is on AND the hub runs in a cluster with the chart-provided
// identity env; anything else falls back to the logging no-op so compose
// stacks and local runs keep working (design/2026-07-27-collection-control-plane.md).
func collectionApplier(enabled bool) collection.Applier {
	if !enabled {
		return collection.NoopApplier{}
	}
	ns := os.Getenv("AVURUOBS_RELEASE_NAMESPACE")
	release := os.Getenv("AVURUOBS_RELEASE_NAME")
	fullname := os.Getenv("AVURUOBS_COLLECTION_FULLNAME")
	if ns == "" || release == "" || fullname == "" {
		slog.Warn("collection runtime control enabled but release identity env missing — overlay will persist without applying",
			"namespace", ns, "release", release, "fullname", fullname)
		return collection.NoopApplier{}
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		slog.Warn("collection runtime control enabled but not running in-cluster — overlay will persist without applying", "error", err)
		return collection.NoopApplier{}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		slog.Warn("collection runtime control: building kubernetes client failed", "error", err)
		return collection.NoopApplier{}
	}
	slog.Info("collection runtime control: cluster applier active",
		"namespace", ns, "release", release, "fullname", fullname)
	return &collection.K8sApplier{Client: client, Namespace: ns, ReleaseName: release, Fullname: fullname}
}

// originCheckMode reads AVURUOBS_AUTH_ORIGIN_CHECK (enforce | log | off).
// Anything below enforce is announced at boot — a CSRF check that has been
// disarmed must never be a silent one. A typo fails the boot rather than
// quietly reverting to enforce: the install would then 403 every write while
// believing it had lowered the check.
func originCheckMode() (string, error) {
	mode := envOr("AVURUOBS_AUTH_ORIGIN_CHECK", api.OriginCheckEnforce)
	switch mode {
	case api.OriginCheckEnforce:
	case api.OriginCheckLog, api.OriginCheckOff:
		slog.Warn("CSRF origin check lowered from enforce — auth.trustedOrigins is the narrow fix and keeps the check on",
			"mode", mode)
	default:
		return "", fmt.Errorf("AVURUOBS_AUTH_ORIGIN_CHECK must be %q, %q or %q, got %q",
			api.OriginCheckEnforce, api.OriginCheckLog, api.OriginCheckOff, mode)
	}
	return mode, nil
}

// trustedOrigins is the CSRF allowlist: AVURUOBS_AUTH_TRUSTED_ORIGINS, plus
// AVURUOBS_PUBLIC_URL when set — an install that already declares its external
// base URL (for the OIDC redirect) should not have to repeat it here.
func trustedOrigins() []string {
	list := splitCSV(envOr("AVURUOBS_AUTH_TRUSTED_ORIGINS", ""))
	if pub := strings.TrimSpace(os.Getenv("AVURUOBS_PUBLIC_URL")); pub != "" {
		list = append(list, pub)
	}
	return list
}

// splitCSV parses a comma-separated env value, trimming blanks (used for
// AVURUOBS_PROJECTS, the config-defined project list).
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
