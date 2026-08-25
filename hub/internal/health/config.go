// Package health computes consolidated service-health groups from the RED
// telemetry the hub already stores — no active probing, no new schema. It is
// pure logic: it takes ServiceStats / ServiceLabel / ServiceEdge values and a
// Config, and returns group health. All SQL lives in the storage backend; all
// HTTP lives in the api package. See design/2026-07-17-service-health-groups.md.
package health

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Tier is a service's criticality class. T0 is the most critical; a dependency
// on a T0 service is what the propagation rule treats as "critical".
type Tier string

const (
	TierT0 Tier = "T0"
	TierT1 Tier = "T1"
	TierT2 Tier = "T2"
	TierT3 Tier = "T3"
)

// knownTiers is the closed set accepted by Validate.
var knownTiers = map[Tier]bool{TierT0: true, TierT1: true, TierT2: true, TierT3: true}

// Selector matches services into a group. A service matches if its name is in
// Services, or its namespace (k8s.namespace.name, falling back to
// service.namespace) is in Namespaces. Empty selector matches nothing.
type Selector struct {
	Namespaces []string `json:"namespaces,omitempty"`
	Services   []string `json:"services,omitempty"`
}

// Check is a scheduled HTTP probe attached to a group — the one signal
// avuru-obs cannot derive from observed traffic: what happens when there is no
// traffic. A group with no spans in the freshness window is either idle or
// dead, and only a probe tells the two apart at 3 a.m.
// See design/2026-07-20-endpoint-checks.md.
type Check struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Interval string `json:"interval,omitempty"` // Go duration; defaults to DefaultCheckInterval
	Expect   Expect `json:"expect,omitempty"`
}

// Expect is what a healthy response looks like. Both fields are optional: with
// neither set, any 2xx inside the timeout passes.
type Expect struct {
	Status     int    `json:"status,omitempty"`
	MaxLatency string `json:"maxLatency,omitempty"` // Go duration
}

// Group is a named set of services with a criticality tier, and optionally the
// probes that answer for it when nothing is calling it.
type Group struct {
	Name     string   `json:"name"`
	Tier     Tier     `json:"tier"`
	Selector Selector `json:"selector"`
	Checks   []Check  `json:"checks,omitempty"`
}

// Thresholds are the SLO-lite knobs for the per-service status rule. Zero
// values mean "inherit" during resolution; ResolveThresholds fills them.
type Thresholds struct {
	ErrorRateWarn         float64 `json:"errorRateWarn,omitempty"`
	ErrorRateCrit         float64 `json:"errorRateCrit,omitempty"`
	LatencyP95ObjectiveMs float64 `json:"latencyP95ObjectiveMs,omitempty"`
	MinSampleCount        uint64  `json:"minSampleCount,omitempty"`
}

// ThresholdConfig holds the precedence layers: global defaults, per-tier
// overrides, per-service overrides (service > tier > default).
type ThresholdConfig struct {
	Defaults Thresholds            `json:"defaults"`
	Tiers    map[Tier]Thresholds   `json:"tiers,omitempty"`
	Services map[string]Thresholds `json:"services,omitempty"`
}

// CriticalEdge force-marks a dependency edge critical even when the target
// isn't T0 (the default criticality rule). Rarely needed.
type CriticalEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Config is the whole service-health configuration, loaded from the mounted
// ConfigMap (AVURUOBS_GROUPS_CONFIG) or Default() when absent.
type Config struct {
	DefaultTier   Tier            `json:"defaultTier"`
	Groups        []Group         `json:"groups,omitempty"`
	Thresholds    ThresholdConfig `json:"thresholds"`
	CriticalEdges []CriticalEdge  `json:"criticalEdges,omitempty"`
	// TierOverrides is the operator's per-service tier, winning over a declared
	// avuru.tier and over a matched group's tier. It exists because a config
	// group is the only other override and it also forces group membership: an
	// operator must be able to correct one service's tier without renaming its
	// group.
	TierOverrides map[string]Tier `json:"tierOverrides,omitempty"`
}

// builtinDefaults are the SLO-lite thresholds applied when config omits them.
// Documented in the AEP; conservative so a quiet or slightly-erroring service
// reads correctly rather than alarmingly.
var builtinDefaults = Thresholds{
	ErrorRateWarn:         0.01,
	ErrorRateCrit:         0.05,
	LatencyP95ObjectiveMs: 500,
	MinSampleCount:        5,
}

// Default is the zero-config configuration: no groups (services auto-group by
// namespace), DefaultTier T2, built-in thresholds. Mirrors modules.Parse("")
// and an empty AVURUOBS_PROJECTS — a working, sensible view with no config.
func Default() Config {
	return Config{
		DefaultTier: TierT2,
		Thresholds:  ThresholdConfig{Defaults: builtinDefaults},
	}
}

// ParseConfig unmarshals the JSON config and validates it fail-loud (a bad
// config crashes the hub at startup, like modules.Parse on a typo, rather than
// silently misgrouping). Empty input yields Default().
func ParseConfig(data []byte) (Config, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return Default(), nil
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parsing service-health config: %w", err)
	}
	if c.DefaultTier == "" {
		c.DefaultTier = TierT2
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	c.normalize()
	return c, nil
}

// Validate enforces the closed tier set, non-empty/unique group names, and
// non-empty selectors — the mistakes that would otherwise misgroup silently.
func (c Config) Validate() error {
	if !knownTiers[c.DefaultTier] {
		return fmt.Errorf("invalid defaultTier %q (known: T0, T1, T2, T3)", c.DefaultTier)
	}
	seen := map[string]bool{}
	// Check IDs are global, not per-group: they key the results table and the
	// /checks/{id}/results route, so two groups cannot share one.
	checkIDs := map[string]bool{}
	for i, g := range c.Groups {
		if strings.TrimSpace(g.Name) == "" {
			return fmt.Errorf("group #%d has an empty name", i)
		}
		if seen[g.Name] {
			return fmt.Errorf("duplicate group name %q", g.Name)
		}
		seen[g.Name] = true
		if !knownTiers[g.Tier] {
			return fmt.Errorf("group %q has invalid tier %q (known: T0, T1, T2, T3)", g.Name, g.Tier)
		}
		if len(g.Selector.Namespaces) == 0 && len(g.Selector.Services) == 0 {
			return fmt.Errorf("group %q has an empty selector (needs namespaces or services)", g.Name)
		}
		for j, ck := range g.Checks {
			if err := ck.validate(g.Name, j, checkIDs); err != nil {
				return err
			}
		}
	}
	for t := range c.Thresholds.Tiers {
		if !knownTiers[t] {
			return fmt.Errorf("thresholds.tiers has invalid tier %q", t)
		}
	}
	for svc, t := range c.TierOverrides {
		if !knownTiers[t] {
			return fmt.Errorf("tierOverrides[%q] has invalid tier %q (known: T0, T1, T2, T3)", svc, t)
		}
	}
	return nil
}

// DefaultCheckInterval is how often a check runs when it does not say. Chosen
// to be frequent enough that a dead group is noticed within a couple of
// minutes, and slow enough that a probe is never mistaken for load.
const DefaultCheckInterval = 60 * time.Second

// DefaultCheckTimeout bounds a single probe. A check that hangs must fail, not
// stall the scheduler behind it.
const DefaultCheckTimeout = 10 * time.Second

func (ck Check) validate(group string, index int, ids map[string]bool) error {
	if strings.TrimSpace(ck.ID) == "" {
		return fmt.Errorf("group %q check #%d has an empty id", group, index)
	}
	if ids[ck.ID] {
		return fmt.Errorf("duplicate check id %q", ck.ID)
	}
	ids[ck.ID] = true
	u, err := url.Parse(ck.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("check %q needs an absolute http(s) url, got %q", ck.ID, ck.URL)
	}
	if _, err := ck.IntervalOrDefault(); err != nil {
		return fmt.Errorf("check %q has an invalid interval %q: %w", ck.ID, ck.Interval, err)
	}
	if _, err := ck.MaxLatencyOrZero(); err != nil {
		return fmt.Errorf("check %q has an invalid expect.maxLatency %q: %w", ck.ID, ck.Expect.MaxLatency, err)
	}
	if s := ck.Expect.Status; s != 0 && (s < 100 || s > 599) {
		return fmt.Errorf("check %q expects status %d, which is not an HTTP status", ck.ID, s)
	}
	return nil
}

// IntervalOrDefault parses the configured interval, falling back to
// DefaultCheckInterval.
func (ck Check) IntervalOrDefault() (time.Duration, error) {
	if strings.TrimSpace(ck.Interval) == "" {
		return DefaultCheckInterval, nil
	}
	d, err := time.ParseDuration(ck.Interval)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("interval must be positive")
	}
	return d, nil
}

// MaxLatencyOrZero parses expect.maxLatency; zero means "no latency
// expectation", which is not the same as "must be instant".
func (ck Check) MaxLatencyOrZero() (time.Duration, error) {
	if strings.TrimSpace(ck.Expect.MaxLatency) == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(ck.Expect.MaxLatency)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("expect.maxLatency must be positive")
	}
	return d, nil
}

// AllChecks flattens every configured check with the group it answers for.
func (c Config) AllChecks() []GroupCheck {
	var out []GroupCheck
	for _, g := range c.Groups {
		for _, ck := range g.Checks {
			out = append(out, GroupCheck{Group: g.Name, Tier: g.Tier, Check: ck})
		}
	}
	return out
}

// GroupCheck is one check plus the group whose health it feeds.
type GroupCheck struct {
	Group string
	Tier  Tier
	Check Check
}

// normalize fills the global default thresholds where unset so ResolveThresholds
// always has a complete base layer.
func (c *Config) normalize() {
	c.Thresholds.Defaults = mergeThresholds(builtinDefaults, c.Thresholds.Defaults)
}

// ResolveThresholds returns the effective thresholds for a (service, tier),
// applying precedence service > tier > global default.
func (c Config) ResolveThresholds(service string, tier Tier) Thresholds {
	t := mergeThresholds(builtinDefaults, c.Thresholds.Defaults)
	if tv, ok := c.Thresholds.Tiers[tier]; ok {
		t = mergeThresholds(t, tv)
	}
	if sv, ok := c.Thresholds.Services[service]; ok {
		t = mergeThresholds(t, sv)
	}
	return t
}

// mergeThresholds overlays non-zero fields of over onto base.
func mergeThresholds(base, over Thresholds) Thresholds {
	if over.ErrorRateWarn != 0 {
		base.ErrorRateWarn = over.ErrorRateWarn
	}
	if over.ErrorRateCrit != 0 {
		base.ErrorRateCrit = over.ErrorRateCrit
	}
	if over.LatencyP95ObjectiveMs != 0 {
		base.LatencyP95ObjectiveMs = over.LatencyP95ObjectiveMs
	}
	if over.MinSampleCount != 0 {
		base.MinSampleCount = over.MinSampleCount
	}
	return base
}

// criticalOverrides indexes CriticalEdges for O(1) lookup by (from,to).
func (c Config) criticalOverrides() map[[2]string]bool {
	if len(c.CriticalEdges) == 0 {
		return nil
	}
	m := make(map[[2]string]bool, len(c.CriticalEdges))
	for _, e := range c.CriticalEdges {
		m[[2]string{e.From, e.To}] = true
	}
	return m
}
