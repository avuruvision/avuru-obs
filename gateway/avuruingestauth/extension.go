package avuruingestauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensionauth"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// Compile-time guarantees: the extension is a server authenticator (satisfied
// structurally by Authenticate) and a component (Start/Shutdown). The
// extensionauth.Server assertion is load-bearing — configauth type-asserts it
// at runtime, so a signature drift must fail the build, not the collector boot.
var (
	_ extensionauth.Server = (*avuruIngestAuth)(nil)
	_ extension.Extension  = (*avuruIngestAuth)(nil)
)

// avuruIngestAuth implements extensionauth.Server (via the Authenticate method)
// and extension.Extension (via Start/Shutdown). One instance per configured
// extension name.
type avuruIngestAuth struct {
	logger     *zap.Logger
	httpClient *http.Client
	hubURL     string
	token      string
	mode       string
	cacheTTL   time.Duration
	staleGrace time.Duration

	mu    sync.Mutex
	cache map[string]cachedVerdict

	// now is time.Now, overridable in tests for cache/stale-grace assertions.
	now func() time.Time

	denied metric.Int64Counter // avuru_ingest_auth_denied_total; nil if unavailable
}

// cachedVerdict is one hub answer plus when we fetched it. A negative verdict
// (valid=false) is cached too, so a flood of bad keys is not a hub DDoS.
type cachedVerdict struct {
	valid   bool
	project string
	fetched time.Time
}

type validateRequest struct {
	Key string `json:"key"`
}

type validateResponse struct {
	Valid   bool   `json:"valid"`
	Project string `json:"project"`
}

var (
	errMissingKey  = errors.New("missing ingest key")
	errInvalidKey  = errors.New("invalid ingest key")
	errValidateDwn = errors.New("ingest key validation unavailable")
)

func newExtension(cfg *Config, set extension.Settings) (*avuruIngestAuth, error) {
	a := &avuruIngestAuth{
		logger:     set.Logger,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		hubURL:     cfg.HubValidateURL,
		token:      string(cfg.InternalToken),
		mode:       cfg.Mode,
		cacheTTL:   cfg.CacheTTL,
		staleGrace: cfg.StaleGrace,
		cache:      map[string]cachedVerdict{},
		now:        time.Now,
	}
	if mp := set.MeterProvider; mp != nil {
		meter := mp.Meter("github.com/avuru/avuru-obs/gateway/avuruingestauth")
		c, err := meter.Int64Counter(
			"avuru_ingest_auth_denied_total",
			metric.WithDescription("OTLP requests denied (enforce) or would-be-denied (log) by the ingest-key authenticator"),
		)
		if err != nil {
			return nil, fmt.Errorf("create denied counter: %w", err)
		}
		a.denied = c
	}
	return a, nil
}

// Start implements component.Component. No background work — validation is
// synchronous on the request path with an in-memory cache.
func (a *avuruIngestAuth) Start(context.Context, component.Host) error { return nil }

// Shutdown implements component.Component.
func (a *avuruIngestAuth) Shutdown(context.Context) error { return nil }

// Authenticate implements extensionauth.Server. sources is the request's
// headers (HTTP: canonical keys; gRPC: lowercase metadata) plus any configured
// query params, so key extraction is case-insensitive. The returned context
// carries the validated project as client auth data in enforce mode.
func (a *avuruIngestAuth) Authenticate(ctx context.Context, sources map[string][]string) (context.Context, error) {
	if a.mode == ModeOff {
		return ctx, nil
	}
	key := extractKey(sources)
	if key == "" {
		return a.deny(ctx, errMissingKey)
	}
	v, err := a.verdict(ctx, key)
	if err != nil {
		// Hub unreachable and no cached verdict within the stale-grace window.
		a.logger.Warn("ingest key validation unavailable", zap.Error(err))
		if a.mode == ModeEnforce {
			return a.deny(ctx, errValidateDwn) // fail closed
		}
		return ctx, nil // log/off: fail open — never drop traffic on a hub blip
	}
	if !v.valid {
		return a.deny(ctx, errInvalidKey)
	}
	// Valid key. Only enforce mode attaches the project — so log mode stays
	// byte-identical to today (tenantfromauth sees no attribute, passes through).
	if a.mode == ModeEnforce {
		info := client.FromContext(ctx)
		info.Auth = ingestAuthData{project: v.project}
		return client.NewContext(ctx, info), nil
	}
	return ctx, nil
}

// deny records the would-be denial and, in enforce mode, returns the error that
// confighttp maps to HTTP 401. In log mode it counts and accepts.
func (a *avuruIngestAuth) deny(ctx context.Context, reason error) (context.Context, error) {
	if a.denied != nil {
		a.denied.Add(ctx, 1, metric.WithAttributes())
	}
	if a.mode == ModeEnforce {
		return ctx, reason
	}
	return ctx, nil
}

// verdict returns a cached verdict when fresh, otherwise validates against the
// hub. On a hub error it serves a stale cached verdict within staleGrace; past
// that it returns the error (the caller decides fail-open vs fail-closed).
func (a *avuruIngestAuth) verdict(ctx context.Context, key string) (cachedVerdict, error) {
	now := a.now()

	a.mu.Lock()
	cached, ok := a.cache[key]
	a.mu.Unlock()

	if ok && now.Sub(cached.fetched) < a.cacheTTL {
		return cached, nil
	}

	fresh, err := a.callHub(ctx, key)
	if err != nil {
		if ok && now.Sub(cached.fetched) < a.staleGrace {
			return cached, nil // stale beats dropping traffic through a hub blip
		}
		return cachedVerdict{}, err
	}
	fresh.fetched = now

	a.mu.Lock()
	a.cache[key] = fresh
	a.mu.Unlock()
	return fresh, nil
}

func (a *avuruIngestAuth) callHub(ctx context.Context, key string) (cachedVerdict, error) {
	body, err := json.Marshal(validateRequest{Key: key})
	if err != nil {
		return cachedVerdict{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.hubURL, bytes.NewReader(body))
	if err != nil {
		return cachedVerdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return cachedVerdict{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return cachedVerdict{}, fmt.Errorf("hub validate: status %d", resp.StatusCode)
	}
	var vr validateResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return cachedVerdict{}, err
	}
	return cachedVerdict{valid: vr.Valid, project: vr.Project}, nil
}

// extractKey reads the ingest key from the Authorization bearer token or the
// X-Avuru-Api-Key header, case-insensitively (HTTP canonical vs gRPC lowercase).
func extractKey(sources map[string][]string) string {
	if v := firstHeader(sources, "authorization"); v != "" {
		if len(v) >= 7 && strings.EqualFold(v[:7], "bearer ") {
			return strings.TrimSpace(v[7:])
		}
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(firstHeader(sources, "x-avuru-api-key"))
}

func firstHeader(sources map[string][]string, name string) string {
	for k, vs := range sources {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

// ingestAuthData exposes the validated project to downstream processors via
// client.FromContext(ctx).Auth.GetAttribute("project").
type ingestAuthData struct {
	project string
}

// ProjectAuthAttribute is the auth-data attribute name carrying the validated
// project — the seam between this extension and the tenantfromauth processor.
const ProjectAuthAttribute = "project"

func (d ingestAuthData) GetAttribute(name string) any {
	if name == ProjectAuthAttribute {
		return d.project
	}
	return nil
}

func (d ingestAuthData) GetAttributeNames() []string {
	return []string{ProjectAuthAttribute}
}
