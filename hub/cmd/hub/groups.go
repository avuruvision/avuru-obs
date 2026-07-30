package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/health"
)

// groupsReloadInterval is how often the mounted config file is re-stat'd for
// changes. A ConfigMap edit propagates to the pod within ~a minute; this poll
// applies it without a restart. Cheap: one stat per tick.
const groupsReloadInterval = 15 * time.Second

// loadGroupsConfig loads the service-health config from AVURUOBS_GROUPS_CONFIG
// and returns an accessor for it. An unset path yields health.Default()
// (auto-only grouping). A present-but-invalid file fails loud at startup (like
// AVURUOBS_MODULES) rather than silently misgrouping. When a path is set, a
// background goroutine re-reads it on mtime change so operators can edit the
// ConfigMap without a pod restart; a later bad edit is logged and ignored
// (the last good config stays live).
func loadGroupsConfig(ctx context.Context) (func() health.Config, error) {
	path := os.Getenv("AVURUOBS_GROUPS_CONFIG")
	if path == "" {
		cfg := health.Default()
		return func() health.Config { return cfg }, nil
	}

	cfg, modTime, err := readGroupsConfig(path)
	if err != nil {
		return nil, fmt.Errorf("AVURUOBS_GROUPS_CONFIG: %w", err)
	}
	slog.Info("service-health config loaded", "path", path, "groups", len(cfg.Groups))

	var current atomic.Pointer[health.Config]
	current.Store(&cfg)
	go watchGroupsConfig(ctx, path, modTime, &current)

	return func() health.Config { return *current.Load() }, nil
}

// readGroupsConfig reads and parses the config file, returning it with the
// file's mod time for change detection.
func readGroupsConfig(path string) (health.Config, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return health.Config{}, time.Time{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return health.Config{}, time.Time{}, err
	}
	cfg, err := health.ParseConfig(data)
	if err != nil {
		return health.Config{}, time.Time{}, err
	}
	return cfg, info.ModTime(), nil
}

// watchGroupsConfig polls the config file's mod time and swaps in a re-parsed
// config on change. Parse failures after startup are logged and skipped.
func watchGroupsConfig(ctx context.Context, path string, last time.Time, current *atomic.Pointer[health.Config]) {
	ticker := time.NewTicker(groupsReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				slog.Warn("service-health config stat failed, keeping current", "path", path, "error", err)
				continue
			}
			if !info.ModTime().After(last) {
				continue
			}
			cfg, modTime, err := readGroupsConfig(path)
			if err != nil {
				slog.Warn("service-health config reload rejected, keeping current", "path", path, "error", err)
				last = info.ModTime() // don't retry the same bad content every tick
				continue
			}
			current.Store(&cfg)
			last = modTime
			slog.Info("service-health config reloaded", "path", path, "groups", len(cfg.Groups))
		}
	}
}
