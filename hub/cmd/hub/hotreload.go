package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// configReloadInterval is how often a mounted config file is re-stat'd for
// changes. A ConfigMap edit propagates to the pod within ~a minute; this poll
// applies it without a restart. Cheap: one stat per tick.
const configReloadInterval = 15 * time.Second

// loadHotReload is the one hot-reloading config loader behind
// loadGroupsConfig, loadAlertingConfig and loadGreenConfig. An unset envVar
// yields def. A present-but-invalid file fails loud at startup (like
// AVURUOBS_MODULES) rather than silently running misconfigured. When a path is
// set, a background goroutine re-reads it on mtime change so operators can
// edit the ConfigMap without a pod restart; a later bad edit is logged and
// ignored (the last good config stays live).
//
// name is the log vocabulary ("green config"); attrs supplies the per-config
// log attributes ("budgets", n) for the loaded/reloaded lines.
func loadHotReload[T any](ctx context.Context, envVar, name string, def T, parse func([]byte) (T, error), attrs func(T) []any) (func() T, error) {
	path := os.Getenv(envVar)
	if path == "" {
		return func() T { return def }, nil
	}

	cfg, modTime, err := readHotReload(path, parse)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envVar, err)
	}
	slog.Info(name+" loaded", append([]any{"path", path}, attrs(cfg)...)...)

	var current atomic.Pointer[T]
	current.Store(&cfg)
	go watchHotReload(ctx, path, name, modTime, &current, parse, attrs)

	return func() T { return *current.Load() }, nil
}

// readHotReload reads and parses the config file, returning it with the
// file's mod time for change detection.
func readHotReload[T any](path string, parse func([]byte) (T, error)) (T, time.Time, error) {
	var zero T
	info, err := os.Stat(path)
	if err != nil {
		return zero, time.Time{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, time.Time{}, err
	}
	cfg, err := parse(data)
	if err != nil {
		return zero, time.Time{}, err
	}
	return cfg, info.ModTime(), nil
}

// watchHotReload polls the config file's mod time and swaps in a re-parsed
// config on change. Parse failures after startup are logged and skipped.
func watchHotReload[T any](ctx context.Context, path, name string, last time.Time, current *atomic.Pointer[T], parse func([]byte) (T, error), attrs func(T) []any) {
	ticker := time.NewTicker(configReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				slog.Warn(name+" stat failed, keeping current", "path", path, "error", err)
				continue
			}
			if !info.ModTime().After(last) {
				continue
			}
			cfg, modTime, err := readHotReload(path, parse)
			if err != nil {
				slog.Warn(name+" reload rejected, keeping current", "path", path, "error", err)
				last = info.ModTime() // don't retry the same bad content every tick
				continue
			}
			current.Store(&cfg)
			last = modTime
			slog.Info(name+" reloaded", append([]any{"path", path}, attrs(cfg)...)...)
		}
	}
}
