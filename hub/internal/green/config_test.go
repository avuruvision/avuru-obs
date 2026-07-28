package green

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseConfigEmptyIsDefault(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t"} {
		got, err := ParseConfig([]byte(in))
		if err != nil {
			t.Fatalf("ParseConfig(%q) error: %v", in, err)
		}
		if got.PUE != 1.5 {
			t.Errorf("ParseConfig(%q) PUE = %v, want 1.5", in, got.PUE)
		}
		if got.BudgetCheckIntervalSec != 300 {
			t.Errorf("ParseConfig(%q) BudgetCheckIntervalSec = %d, want 300", in, got.BudgetCheckIntervalSec)
		}
		if len(got.Budgets) != 0 {
			t.Errorf("ParseConfig(%q) expected no budgets, got %d", in, len(got.Budgets))
		}
		wantMetrics := Metrics{
			PodEnergy:        []string{"kepler_pod_cpu_joules_total"},
			NodeEnergy:       []string{"kepler_node_cpu_joules_total"},
			PodNameAttr:      "pod_name",
			PodNamespaceAttr: "pod_namespace",
		}
		if !reflect.DeepEqual(got.Metrics, wantMetrics) {
			t.Errorf("ParseConfig(%q) Metrics = %+v, want %+v", in, got.Metrics, wantMetrics)
		}
	}
}

func TestParseConfigValidatesFailLoud(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"unknown field", `{"bogus":true}`},
		{"unknown nested metrics field", `{"metrics":{"bogus":"x"}}`},
		{"pue below 1", `{"pue":0.8}`},
		{"negative pue", `{"pue":-1.5}`},
		{"negative gridIntensity", `{"gridIntensity":-10}`},
		{"unknown region without explicit intensity", `{"region":"XX"}`},
		{"negative budgetCheckIntervalSec", `{"budgetCheckIntervalSec":-5}`},
		{"budget empty name", `{"budgets":[{"name":"","group":"core","monthlyKgCO2e":100}]}`},
		{"duplicate budget name", `{"budgets":[
			{"name":"platform","group":"core","monthlyKgCO2e":100},
			{"name":"platform","group":"edge","monthlyKgCO2e":50}]}`},
		{"budget empty group", `{"budgets":[{"name":"platform","group":"","monthlyKgCO2e":100}]}`},
		{"budget zero monthlyKgCO2e", `{"budgets":[{"name":"platform","group":"core","monthlyKgCO2e":0}]}`},
		{"budget negative monthlyKgCO2e", `{"budgets":[{"name":"platform","group":"core","monthlyKgCO2e":-1}]}`},
		{"warnRatio above 1", `{"budgets":[{"name":"platform","group":"core","monthlyKgCO2e":100,"warnRatio":1.2}]}`},
		{"negative warnRatio", `{"budgets":[{"name":"platform","group":"core","monthlyKgCO2e":100,"warnRatio":-0.5}]}`},
		{"empty podEnergy entry", `{"metrics":{"podEnergy":[""]}}`},
		{"whitespace-only podNameAttr", `{"metrics":{"podNameAttr":"   "}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(tt.json)); err == nil {
				t.Errorf("ParseConfig(%s) expected error, got nil", tt.json)
			}
		})
	}
}

func TestParseConfigDefaultsFillUnset(t *testing.T) {
	const cfg = `{
	  "region": "se",
	  "metrics": {"podEnergy": ["kepler_pod_energy_joules_total"]},
	  "budgets": [{"name":"platform","group":"core","monthlyKgCO2e":120,"channel":"ops"}]
	}`
	c, err := ParseConfig([]byte(cfg))
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if c.PUE != 1.5 {
		t.Errorf("PUE default: got %v want 1.5", c.PUE)
	}
	if c.BudgetCheckIntervalSec != 300 {
		t.Errorf("BudgetCheckIntervalSec default: got %d want 300", c.BudgetCheckIntervalSec)
	}
	if c.Region != "SE" {
		t.Errorf("Region should normalize uppercase: got %q want SE", c.Region)
	}
	// Overridden metric list sticks; the untouched names keep their defaults.
	if len(c.Metrics.PodEnergy) != 1 || c.Metrics.PodEnergy[0] != "kepler_pod_energy_joules_total" {
		t.Errorf("PodEnergy override lost: %v", c.Metrics.PodEnergy)
	}
	if len(c.Metrics.NodeEnergy) != 1 || c.Metrics.NodeEnergy[0] != "kepler_node_cpu_joules_total" {
		t.Errorf("NodeEnergy default lost: %v", c.Metrics.NodeEnergy)
	}
	if c.Metrics.PodNameAttr != "pod_name" || c.Metrics.PodNamespaceAttr != "pod_namespace" {
		t.Errorf("attr defaults lost: %+v", c.Metrics)
	}
	if c.Budgets[0].WarnRatio != 0.8 {
		t.Errorf("WarnRatio default: got %v want 0.8", c.Budgets[0].WarnRatio)
	}
	if c.Budgets[0].Channel != "ops" {
		t.Errorf("Channel: got %q want ops", c.Budgets[0].Channel)
	}
}

func TestParseConfigBudgetChannelOptional(t *testing.T) {
	// No channel: budgets degrade to dashboard-only status (the AEP's
	// degradation story with alerting off) — valid config, not an error.
	c, err := ParseConfig([]byte(`{"budgets":[{"name":"platform","group":"core","monthlyKgCO2e":100}]}`))
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if c.Budgets[0].Channel != "" {
		t.Errorf("Channel: got %q want empty", c.Budgets[0].Channel)
	}
}

func TestParseConfigUnknownRegionWithExplicitIntensity(t *testing.T) {
	// A region the table doesn't know is fine when the operator sets the
	// intensity explicitly — the operator value wins and the region is only
	// informational.
	c, err := ParseConfig([]byte(`{"region":"XX","gridIntensity":123}`))
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	v, src := c.EffectiveIntensity()
	if v != 123 || src != "operator-set" {
		t.Errorf("EffectiveIntensity = (%v, %q), want (123, operator-set)", v, src)
	}
}

func TestEffectiveIntensityPrecedence(t *testing.T) {
	// Operator-set beats the region table.
	op := Config{GridIntensity: 250, Region: "SE"}
	if v, src := op.EffectiveIntensity(); v != 250 || src != "operator-set" {
		t.Errorf("operator-set: got (%v, %q)", v, src)
	}

	// Region falls back to the bundled annual average with a precise label.
	se := Config{Region: "SE"}
	v, src := se.EffectiveIntensity()
	if v != countryIntensity["SE"] {
		t.Errorf("SE intensity: got %v want %v", v, countryIntensity["SE"])
	}
	if src != "bundled annual average (SE, Ember 2023/24)" {
		t.Errorf("SE label: got %q", src)
	}

	// No region at all -> world average.
	world := Config{}
	v, src = world.EffectiveIntensity()
	if v != worldIntensity {
		t.Errorf("world intensity: got %v want %v", v, worldIntensity)
	}
	if src != "bundled world average (Ember 2023/24)" {
		t.Errorf("world label: got %q", src)
	}

	// Explicit WORLD region reads as the world average, not a country.
	if v, src := (Config{Region: "WORLD"}).EffectiveIntensity(); v != worldIntensity || !strings.Contains(src, "world") {
		t.Errorf("WORLD region: got (%v, %q)", v, src)
	}
}

func TestParseConfigValid(t *testing.T) {
	const cfg = `{
	  "region": "FR",
	  "pue": 1.2,
	  "metrics": {
	    "podEnergy": ["kepler_pod_cpu_joules_total", "kepler_pod_energy_joules_total"],
	    "nodeEnergy": ["kepler_node_cpu_joules_total"],
	    "podNameAttr": "pod",
	    "podNamespaceAttr": "namespace"
	  },
	  "budgets": [
	    {"name":"platform","group":"core","monthlyKgCO2e":120,"warnRatio":0.9,"channel":"ops"},
	    {"name":"edge","group":"edge","monthlyKgCO2e":40}
	  ],
	  "budgetCheckIntervalSec": 60
	}`
	c, err := ParseConfig([]byte(cfg))
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if c.PUE != 1.2 {
		t.Errorf("PUE: got %v want 1.2", c.PUE)
	}
	if c.BudgetCheckIntervalSec != 60 {
		t.Errorf("BudgetCheckIntervalSec: got %d want 60", c.BudgetCheckIntervalSec)
	}
	if len(c.Metrics.PodEnergy) != 2 {
		t.Errorf("PodEnergy: got %v", c.Metrics.PodEnergy)
	}
	if c.Metrics.PodNameAttr != "pod" || c.Metrics.PodNamespaceAttr != "namespace" {
		t.Errorf("attrs: %+v", c.Metrics)
	}
	if len(c.Budgets) != 2 || c.Budgets[0].WarnRatio != 0.9 || c.Budgets[1].WarnRatio != 0.8 {
		t.Errorf("budgets: %+v", c.Budgets)
	}
	v, src := c.EffectiveIntensity()
	if v != countryIntensity["FR"] || src != "bundled annual average (FR, Ember 2023/24)" {
		t.Errorf("EffectiveIntensity = (%v, %q)", v, src)
	}
}

func TestParseConfigTrimsMetricNames(t *testing.T) {
	const cfg = `{"metrics":{
	  "podEnergy": ["  kepler_pod_energy_joules_total "],
	  "podNameAttr": " pod "
	}}`
	c, err := ParseConfig([]byte(cfg))
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if got := c.Metrics.PodEnergy[0]; got != "kepler_pod_energy_joules_total" {
		t.Errorf("podEnergy not trimmed: %q", got)
	}
	if c.Metrics.PodNameAttr != "pod" {
		t.Errorf("podNameAttr not trimmed: %q", c.Metrics.PodNameAttr)
	}
}
