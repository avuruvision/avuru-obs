package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/green"
)

// greenReloadInterval is how often the mounted config file is re-stat'd for
// changes. A ConfigMap edit propagates to the pod within ~a minute; this poll
// applies it without a restart. Cheap: one stat per tick.
const greenReloadInterval = 15 * time.Second

// loadGreenConfig loads the green (energy/carbon) config from
// AVURUOBS_GREEN_CONFIG and returns an accessor for it. An unset path yields
// green.Default() (world-average factors, Kepler default metric names, no
// budgets). A present-but-invalid file fails loud at startup (like
// AVURUOBS_MODULES) rather than silently misreporting carbon. When a path is
// set, a background goroutine re-reads it on mtime change so operators can
// edit the ConfigMap without a pod restart; a later bad edit is logged and
// ignored (the last good config stays live).
func loadGreenConfig(ctx context.Context) (func() green.Config, error) {
	path := os.Getenv("AVURUOBS_GREEN_CONFIG")
	if path == "" {
		cfg := green.Default()
		return func() green.Config { return cfg }, nil
	}

	cfg, modTime, err := readGreenConfig(path)
	if err != nil {
		return nil, fmt.Errorf("AVURUOBS_GREEN_CONFIG: %w", err)
	}
	slog.Info("green config loaded", "path", path, "budgets", len(cfg.Budgets))

	var current atomic.Pointer[green.Config]
	current.Store(&cfg)
	go watchGreenConfig(ctx, path, modTime, &current)

	return func() green.Config { return *current.Load() }, nil
}

// readGreenConfig reads and parses the config file, returning it with the
// file's mod time for change detection.
func readGreenConfig(path string) (green.Config, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return green.Config{}, time.Time{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return green.Config{}, time.Time{}, err
	}
	cfg, err := green.ParseConfig(data)
	if err != nil {
		return green.Config{}, time.Time{}, err
	}
	return cfg, info.ModTime(), nil
}

// watchGreenConfig polls the config file's mod time and swaps in a re-parsed
// config on change. Parse failures after startup are logged and skipped.
func watchGreenConfig(ctx context.Context, path string, last time.Time, current *atomic.Pointer[green.Config]) {
	ticker := time.NewTicker(greenReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				slog.Warn("green config stat failed, keeping current", "path", path, "error", err)
				continue
			}
			if !info.ModTime().After(last) {
				continue
			}
			cfg, modTime, err := readGreenConfig(path)
			if err != nil {
				slog.Warn("green config reload rejected, keeping current", "path", path, "error", err)
				last = info.ModTime() // don't retry the same bad content every tick
				continue
			}
			current.Store(&cfg)
			last = modTime
			slog.Info("green config reloaded", "path", path, "budgets", len(cfg.Budgets))
		}
	}
}
