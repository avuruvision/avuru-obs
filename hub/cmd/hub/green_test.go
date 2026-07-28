package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadGreenConfigUnsetIsDefault: no AVURUOPS_GREEN_CONFIG → static
// defaults, no polling.
func TestLoadGreenConfigUnsetIsDefault(t *testing.T) {
	t.Setenv("AVURUOPS_GREEN_CONFIG", "")
	get, err := loadGreenConfig(context.Background())
	if err != nil {
		t.Fatalf("loadGreenConfig: %v", err)
	}
	cfg := get()
	if cfg.PUE != 1.5 || cfg.BudgetCheckIntervalSec != 300 || len(cfg.Budgets) != 0 {
		t.Errorf("expected green.Default(), got %+v", cfg)
	}
}

// TestLoadGreenConfigFromFile: a valid mounted file parses, applies defaults,
// and the accessor serves it.
func TestLoadGreenConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "green.json")
	content := `{"region":"SE","budgets":[{"name":"platform","group":"core","monthlyKgCO2e":120}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("AVURUOPS_GREEN_CONFIG", path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // stops the watcher goroutine

	get, err := loadGreenConfig(ctx)
	if err != nil {
		t.Fatalf("loadGreenConfig: %v", err)
	}
	cfg := get()
	if cfg.Region != "SE" {
		t.Errorf("Region: got %q want SE", cfg.Region)
	}
	if len(cfg.Budgets) != 1 || cfg.Budgets[0].WarnRatio != 0.8 {
		t.Errorf("budget defaults not applied: %+v", cfg.Budgets)
	}
	if v, src := cfg.EffectiveIntensity(); v <= 0 || src == "" {
		t.Errorf("EffectiveIntensity: got (%v, %q)", v, src)
	}
}

// TestLoadGreenConfigInvalidFailsLoud: a present-but-invalid file is a startup
// error (like AVURUOPS_MODULES), never a silent default.
func TestLoadGreenConfigInvalidFailsLoud(t *testing.T) {
	path := filepath.Join(t.TempDir(), "green.json")
	if err := os.WriteFile(path, []byte(`{"region":"XX"}`), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("AVURUOPS_GREEN_CONFIG", path)
	if _, err := loadGreenConfig(context.Background()); err == nil {
		t.Fatal("expected error for unknown region, got nil")
	}
}
