// Package green computes per-service energy (Wh) and carbon (gCO2e) from the
// Kepler metrics the sensor ships into the existing otel_metrics_* tables. It
// is pure logic like health/alerting: all SQL lives in the storage backend,
// all HTTP in the api package. See design/2026-07-22-green-carbon.md.
package green

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Metrics names the Kepler series and attributes the read path queries.
// Config-driven because the rebooted Kepler's naming is a verify item (the
// AEP's documented v1 limitations): operators can rename without a rebuild.
type Metrics struct {
	// PodEnergy are the cumulative per-pod CPU energy counters (joules).
	PodEnergy []string `json:"podEnergy,omitempty"`
	// NodeEnergy are the cumulative per-node CPU energy counters (joules).
	NodeEnergy []string `json:"nodeEnergy,omitempty"`
	// PodNameAttr / PodNamespaceAttr are the metric attributes carrying pod
	// identity, joined against the kubeletstats resource attributes.
	PodNameAttr      string `json:"podNameAttr,omitempty"`
	PodNamespaceAttr string `json:"podNamespaceAttr,omitempty"`
}

// Budget is a monthly gCO2e budget for a serviceGroups group. Channel names
// an alerting channel for warn/exceeded notifications; empty (or alerting
// disabled) degrades to dashboard-only status per the AEP, not an error.
type Budget struct {
	Name          string  `json:"name"`
	Group         string  `json:"group"`
	MonthlyKgCO2e float64 `json:"monthlyKgCO2e"`
	// WarnRatio is the warn threshold as a fraction of the budget, in (0, 1].
	// 0 → 0.8 (the AEP's warn-at-80%).
	WarnRatio float64 `json:"warnRatio,omitempty"`
	Channel   string  `json:"channel,omitempty"`
}

// Config is the whole green configuration — carbon factors, metric-name
// overrides, and budgets — loaded from the mounted ConfigMap
// (AVURUOPS_GREEN_CONFIG) or Default() when absent.
type Config struct {
	// Region is an ISO 3166-1 alpha-2 country code selecting a bundled
	// annual-average grid intensity when GridIntensity is unset. Empty →
	// world average.
	Region string `json:"region,omitempty"`
	// GridIntensity is the operator-set grid carbon intensity (gCO2e/kWh);
	// it wins over any bundled value. 0 → bundled lookup.
	GridIntensity float64 `json:"gridIntensity,omitempty"`
	// PUE is the power-usage-effectiveness multiplier applied to measured
	// energy (>= 1.0; 0 → 1.5, a conservative generic-datacenter default).
	PUE     float64  `json:"pue,omitempty"`
	Metrics Metrics  `json:"metrics"`
	Budgets []Budget `json:"budgets,omitempty"`
	// BudgetCheckIntervalSec is how often the budget evaluator runs (0 → 300).
	BudgetCheckIntervalSec int `json:"budgetCheckIntervalSec,omitempty"`
}

const (
	defaultPUE                    = 1.5
	defaultWarnRatio              = 0.8
	defaultBudgetCheckIntervalSec = 300
)

// DefaultWarnRatio is the warn threshold (fraction of budget) applied to a
// budget that sets none. Exported so the API's fallback for direct-constructed
// configs references this single source instead of re-hardcoding 0.8.
const DefaultWarnRatio = defaultWarnRatio

// defaultMetrics are the rebooted-Kepler (v0.11.x) names from the AEP's
// metric-name table — the single source of truth both the sensor keep-regex
// and these defaults must match.
func defaultMetrics() Metrics {
	return Metrics{
		PodEnergy:        []string{"kepler_pod_cpu_joules_total"},
		NodeEnergy:       []string{"kepler_node_cpu_joules_total"},
		PodNameAttr:      "pod_name",
		PodNamespaceAttr: "pod_namespace",
	}
}

// Default is the zero-config configuration: world-average intensity, PUE 1.5,
// Kepler default metric names, no budgets (inert evaluator). Mirrors an absent
// AVURUOPS_GREEN_CONFIG, like health.Default / alerting.Default.
func Default() Config {
	return Config{
		PUE:                    defaultPUE,
		Metrics:                defaultMetrics(),
		BudgetCheckIntervalSec: defaultBudgetCheckIntervalSec,
	}
}

// ParseConfig unmarshals + validates fail-loud (a bad config crashes the hub
// at startup, like modules.Parse, rather than silently misreporting carbon).
// Empty input yields Default(). Note the deliberate normalize-BEFORE-validate
// ordering (the opposite of health.ParseConfig): validation judges effective
// values, so zero-means-default fields (pue, warnRatio, interval) don't need
// zero-vs-absent special cases in Validate — don't "align" this with health.
func ParseConfig(data []byte) (Config, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return Default(), nil
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parsing green config: %w", err)
	}
	c.normalize()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// normalize canonicalizes the region and fills documented defaults where
// unset, so Validate always judges the effective values.
func (c *Config) normalize() {
	c.Region = strings.ToUpper(strings.TrimSpace(c.Region))
	if c.PUE == 0 {
		c.PUE = defaultPUE
	}
	if c.BudgetCheckIntervalSec == 0 {
		c.BudgetCheckIntervalSec = defaultBudgetCheckIntervalSec
	}
	d := defaultMetrics()
	if len(c.Metrics.PodEnergy) == 0 {
		c.Metrics.PodEnergy = d.PodEnergy
	}
	if len(c.Metrics.NodeEnergy) == 0 {
		c.Metrics.NodeEnergy = d.NodeEnergy
	}
	if c.Metrics.PodNameAttr == "" {
		c.Metrics.PodNameAttr = d.PodNameAttr
	}
	if c.Metrics.PodNamespaceAttr == "" {
		c.Metrics.PodNamespaceAttr = d.PodNamespaceAttr
	}
	// Trim AFTER default-fill: absent/empty means "use default", but a
	// whitespace-only value is a config mistake Validate must reject.
	for i := range c.Metrics.PodEnergy {
		c.Metrics.PodEnergy[i] = strings.TrimSpace(c.Metrics.PodEnergy[i])
	}
	for i := range c.Metrics.NodeEnergy {
		c.Metrics.NodeEnergy[i] = strings.TrimSpace(c.Metrics.NodeEnergy[i])
	}
	c.Metrics.PodNameAttr = strings.TrimSpace(c.Metrics.PodNameAttr)
	c.Metrics.PodNamespaceAttr = strings.TrimSpace(c.Metrics.PodNamespaceAttr)
	for i := range c.Budgets {
		if c.Budgets[i].WarnRatio == 0 {
			c.Budgets[i].WarnRatio = defaultWarnRatio
		}
	}
}

// Validate rejects the mistakes that would otherwise misreport carbon or
// silently drop budgets. An unknown region with no explicit intensity is an
// error, not a silent world-average fallback — the fallback is for region
// UNSET, and a typoed country code must fail loud.
func (c Config) Validate() error {
	if c.GridIntensity < 0 {
		return fmt.Errorf("gridIntensity must be >= 0 gCO2e/kWh, got %v", c.GridIntensity)
	}
	if c.PUE < 1.0 {
		return fmt.Errorf("pue must be >= 1.0, got %v", c.PUE)
	}
	if c.Region != "" && c.Region != regionWorld && c.GridIntensity == 0 {
		if _, ok := countryIntensity[c.Region]; !ok {
			return fmt.Errorf("unknown region %q: not in the bundled intensity table — use an ISO 3166-1 alpha-2 code it covers or set gridIntensity explicitly", c.Region)
		}
	}
	if c.BudgetCheckIntervalSec <= 0 {
		return fmt.Errorf("budgetCheckIntervalSec must be > 0, got %d", c.BudgetCheckIntervalSec)
	}
	// Empty metric/attr names (post-trim) would match nothing and silently
	// read as 0 Wh — exactly the misreporting this package must fail loud on.
	for i, m := range c.Metrics.PodEnergy {
		if m == "" {
			return fmt.Errorf("metrics.podEnergy[%d] is empty", i)
		}
	}
	for i, m := range c.Metrics.NodeEnergy {
		if m == "" {
			return fmt.Errorf("metrics.nodeEnergy[%d] is empty", i)
		}
	}
	if c.Metrics.PodNameAttr == "" {
		return fmt.Errorf("metrics.podNameAttr is empty")
	}
	if c.Metrics.PodNamespaceAttr == "" {
		return fmt.Errorf("metrics.podNamespaceAttr is empty")
	}
	seen := map[string]bool{}
	for i, b := range c.Budgets {
		if strings.TrimSpace(b.Name) == "" {
			return fmt.Errorf("budget #%d has an empty name", i)
		}
		if seen[b.Name] {
			return fmt.Errorf("duplicate budget name %q", b.Name)
		}
		seen[b.Name] = true
		if strings.TrimSpace(b.Group) == "" {
			return fmt.Errorf("budget %q has an empty group", b.Name)
		}
		if b.MonthlyKgCO2e <= 0 {
			return fmt.Errorf("budget %q must have monthlyKgCO2e > 0, got %v", b.Name, b.MonthlyKgCO2e)
		}
		if b.WarnRatio <= 0 || b.WarnRatio > 1 {
			return fmt.Errorf("budget %q has warnRatio %v, must be in (0, 1]", b.Name, b.WarnRatio)
		}
	}
	return nil
}

// BudgetCheckInterval is how often the alerting tick recomputes the SQL-backed
// month-to-date usage roll-up for budget evaluation (0 → the 300 s default).
// Budget state advances only when usage is recomputed, so BOTH fire and resolve
// latency are bounded by this interval — unlike health alerts, which re-evaluate
// every tick. Health alerting on the same tick is unaffected; only this usage
// recompute is throttled.
func (c Config) BudgetCheckInterval() time.Duration {
	s := c.BudgetCheckIntervalSec
	if s <= 0 {
		s = defaultBudgetCheckIntervalSec
	}
	return time.Duration(s) * time.Second
}

// EffectiveIntensity returns the grid intensity (gCO2e/kWh) the carbon math
// uses, plus a source label the CSRD methodology block quotes verbatim as
// factor provenance. Precedence: operator-set > bundled region average >
// bundled world average.
func (c Config) EffectiveIntensity() (float64, string) {
	if c.GridIntensity > 0 {
		return c.GridIntensity, "operator-set"
	}
	// Re-normalize on purpose: direct-construction callers skip normalize().
	region := strings.ToUpper(strings.TrimSpace(c.Region))
	if v, ok := countryIntensity[region]; ok {
		return v, fmt.Sprintf("bundled annual average (%s, %s)", region, intensityDataset)
	}
	return worldIntensity, fmt.Sprintf("bundled world average (%s)", intensityDataset)
}
