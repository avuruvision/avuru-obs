package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadTopologyConfigUnsetIsDefault: no AVURUOBS_TOPOLOGY_CONFIG → the
// built-in mesh patterns, no file watched.
func TestLoadTopologyConfigUnsetIsDefault(t *testing.T) {
	t.Setenv("AVURUOBS_TOPOLOGY_CONFIG", "")
	get, err := loadTopologyConfig(context.Background())
	if err != nil {
		t.Fatalf("loadTopologyConfig: %v", err)
	}
	cfg := get()
	if len(cfg.Transport) != 0 || cfg.DisableDefaults {
		t.Errorf("unset env should yield Default(), got %+v", cfg)
	}
	if len(cfg.TransportPatterns()) == 0 {
		t.Error("Default() must still carry the built-in transport patterns")
	}
}

func TestLoadTopologyConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.json")
	if err := os.WriteFile(path, []byte(`{"transport":["mesh-*"],"applications":["waypoint"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AVURUOBS_TOPOLOGY_CONFIG", path)
	get, err := loadTopologyConfig(context.Background())
	if err != nil {
		t.Fatalf("loadTopologyConfig: %v", err)
	}
	cfg := get()
	if len(cfg.Transport) != 1 || cfg.Transport[0] != "mesh-*" {
		t.Errorf("transport = %v", cfg.Transport)
	}
	if len(cfg.Applications) != 1 || cfg.Applications[0] != "waypoint" {
		t.Errorf("applications = %v", cfg.Applications)
	}
}

// A bad config must fail the boot rather than silently misclassifying the map
// (same contract as AVURUOBS_MODULES and the other mounted configs).
func TestLoadTopologyConfigInvalidFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.json")
	if err := os.WriteFile(path, []byte(`{"transport":["[bad"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AVURUOBS_TOPOLOGY_CONFIG", path)
	if _, err := loadTopologyConfig(context.Background()); err == nil {
		t.Fatal("invalid topology config accepted, want a startup error")
	}
}
