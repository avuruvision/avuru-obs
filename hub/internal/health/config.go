// Package health computes consolidated service-health groups from the RED
// telemetry the hub already stores — no active probing, no new schema. It is
// pure logic: it takes ServiceStats / ServiceLabel / ServiceEdge values and a
// Config, and returns group health. All SQL lives in the storage backend; all
// HTTP lives in the api package. See design/2026-07-17-service-health-groups.md.
package health

import (
	"encoding/json"
	"fmt"
	"strings"
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

// Group is a named set of services with a criticality tier.
type Group struct {
	Name     string   `json:"name"`
	Tier     Tier     `json:"tier"`
	Selector Selector `json:"selector"`
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
// ConfigMap (AVURUOPS_GROUPS_CONFIG) or Default() when absent.
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
// and an empty AVURUOPS_PROJECTS — a working, sensible view with no config.
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
