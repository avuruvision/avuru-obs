package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ingestSeedKey is one chart-provisioned ingest key.
//
// Why this exists: in enforce mode the gateway rejects unkeyed OTLP, and
// avuru's own sensor is an unkeyed OTLP sender. Without a key provisioned at
// install time, flipping auth.ingest.mode to enforce would silence the product's
// own agent. The chart generates the sensor's key at render time (it knows the
// raw value; the hub only ever stores the hash) and passes it here.
type ingestSeedKey struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	Key     string `json:"key"`
}

// parseIngestSeedKeys parses AVURUOBS_INGEST_SEED_KEYS — a JSON array of
// {project, name, key}. Empty input means "no seed keys", not an error.
//
// Malformed entries are rejected loudly rather than skipped: a silently dropped
// seed would look like a healthy enforce install right up until the operator
// noticed telemetry had stopped landing.
func parseIngestSeedKeys(raw string) ([]ingestSeedKey, error) {
	if raw == "" {
		return nil, nil
	}
	var keys []ingestSeedKey
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("parse ingest seed keys: %w", err)
	}
	for i, k := range keys {
		if k.Project == "" {
			return nil, fmt.Errorf("ingest seed key %d: project is required", i)
		}
		if k.Key == "" {
			return nil, fmt.Errorf("ingest seed key %d (project %q): key is required", i, k.Project)
		}
	}
	return keys, nil
}

// seedIngestKeys waits for the schema, then idempotently ensures each seed key
// exists. Same degraded-not-crashed philosophy as bootstrapAdmin: seeding can
// still fail transiently after the tables exist, so this retries rather than
// giving up.
//
// Idempotency is by key hash, so re-running on every hub restart and across
// upgrades inserts nothing new. A key the operator revoked through the UI stays
// revoked — GetIngestKeyByHash treats revoked as not-found, so a revoked seed
// key WOULD be re-created here; the chart therefore only seeds keys it owns
// (the sensor's), and operator-managed keys are created through the API.
func seedIngestKeys(ctx context.Context, provider api.StoreProvider, gate *schemaGate, keys []ingestSeedKey) {
	if len(keys) == 0 {
		return
	}

	if !gate.wait(ctx) {
		return
	}

	pending := make([]ingestSeedKey, len(keys))
	copy(pending, keys)

	start := time.Now()
	erroredOnce := false
	for len(pending) > 0 {
		st := provider()
		if st == nil {
			return
		}
		var failed []ingestSeedKey
		var lastErr error
		for _, k := range pending {
			if err := ensureIngestKey(ctx, st, k); err != nil {
				failed = append(failed, k)
				lastErr = err
				continue
			}
		}
		pending = failed
		if len(pending) == 0 {
			slog.Info("ingest seed keys ready", "count", len(keys))
			return
		}

		// Never log k.Key — the raw secret must not reach the logs.
		slog.Warn("ingest key seeding failed, retrying", "remaining", len(pending), "error", lastErr)
		elapsed := time.Since(start)
		if !erroredOnce && elapsed >= 2*time.Minute {
			slog.Error("ingest key seeding still failing after 2 minutes — check ClickHouse/migration state", "error", lastErr)
			erroredOnce = true
		}
		interval := 5 * time.Second
		if elapsed >= 2*time.Minute {
			interval = 60 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// ensureIngestKey creates the key when no live key with that hash exists.
func ensureIngestKey(ctx context.Context, st storage.Store, k ingestSeedKey) error {
	hash := auth.HashIngestKey(k.Key)
	_, err := st.GetIngestKeyByHash(ctx, hash)
	if err == nil {
		return nil // already seeded
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	prefix := k.Key
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	name := k.Name
	if name == "" {
		name = "chart-provisioned"
	}
	return st.CreateIngestKey(ctx, storage.AuthIngestKey{
		KeyHash:   hash,
		Project:   k.Project,
		Name:      name,
		Prefix:    prefix,
		CreatedBy: "chart",
	})
}
