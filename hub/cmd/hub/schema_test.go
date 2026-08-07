package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// fakeSchemaStore drives the gate without a ClickHouse container: it hands out
// one scripted status per check, and records every Migrate call.
type fakeSchemaStore struct {
	mu       sync.Mutex
	statuses []storage.SchemaStatus
	errs     []error
	checks   int
	migrates int
	// migrateErr, when set, fails every Migrate.
	migrateErr error
}

func (f *fakeSchemaStore) SchemaStatus(context.Context, modules.Set) (storage.SchemaStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.checks
	f.checks++
	if i < len(f.errs) && f.errs[i] != nil {
		return storage.SchemaStatus{}, f.errs[i]
	}
	if i >= len(f.statuses) {
		return f.statuses[len(f.statuses)-1], nil // hold the last scripted state
	}
	return f.statuses[i], nil
}

func (f *fakeSchemaStore) Migrate(context.Context, modules.Set) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.migrates++
	return f.migrateErr
}

func (f *fakeSchemaStore) counts() (checks, migrates int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks, f.migrates
}

func ready() storage.SchemaStatus {
	return storage.SchemaStatus{Ready: true, Database: "otel", Expected: []string{"a"}, Applied: []string{"a"}}
}

func notReady() storage.SchemaStatus {
	return storage.SchemaStatus{Database: "otel", Expected: []string{"a"}, Missing: []string{"a"}}
}

// newTestGate is the production constructor with the poll shortened, so tests
// exercise the real retry pacing without sleeping for 30s a time.
func newTestGate(auto bool) *schemaGate {
	g := newSchemaGate(modules.AllSet(), "otel", auto)
	g.recheck = 5 * time.Millisecond
	return g
}

// captureLogs swaps the default slog logger for the duration of fn.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

func TestSchemaGateOpensWhenReady(t *testing.T) {
	store := &fakeSchemaStore{statuses: []storage.SchemaStatus{ready()}}
	gate := newTestGate(true)

	gate.run(context.Background(), store)

	if !gate.wait(context.Background()) {
		t.Fatal("gate did not open on a ready schema")
	}
	if _, migrates := store.counts(); migrates != 0 {
		t.Errorf("Migrate called %d times on a ready schema, want 0", migrates)
	}
	if st := gate.Status(); !st.Ready {
		t.Errorf("Status().Ready = false, want true")
	}
}

func TestSchemaGateSelfHeals(t *testing.T) {
	// Missing on the first check, present after the gate applies migrations —
	// the incident this whole mechanism exists for.
	store := &fakeSchemaStore{statuses: []storage.SchemaStatus{notReady(), ready()}}
	gate := newTestGate(true)

	gate.run(context.Background(), store)

	if !gate.wait(context.Background()) {
		t.Fatal("gate did not open after self-heal")
	}
	if _, migrates := store.counts(); migrates != 1 {
		t.Errorf("Migrate called %d times, want exactly 1", migrates)
	}
}

func TestSchemaGateNeverAppliesWhenAutoMigrateOff(t *testing.T) {
	store := &fakeSchemaStore{statuses: []storage.SchemaStatus{notReady()}}
	gate := newSchemaGate(modules.AllSet(), "otel", false)

	// Never ready, so run only returns on ctx — bound it.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out := captureLogs(t, func() { gate.run(ctx, store) })

	if _, migrates := store.counts(); migrates != 0 {
		t.Errorf("Migrate called %d times with autoMigrate off, want 0", migrates)
	}
	if gate.wait(ctx) {
		t.Error("gate opened despite an incomplete schema")
	}
	if !strings.Contains(out, "AVURUOBS_SCHEMA_AUTOMIGRATE=false") {
		t.Errorf("error does not name the disabled setting:\n%s", out)
	}
}

// The flood is the bug. However long the schema stays broken, operators get one
// ERROR naming the fix — everything after it is quiet.
func TestSchemaGateReportsErrorOnce(t *testing.T) {
	store := &fakeSchemaStore{
		statuses:   []storage.SchemaStatus{notReady()},
		migrateErr: errors.New("no DDL grant"),
	}
	gate := newTestGate(true)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	out := captureLogs(t, func() { gate.run(ctx, store) })

	if n := countLevel(out, "ERROR"); n != 1 {
		t.Errorf("emitted %d ERROR lines, want exactly 1:\n%s", n, out)
	}
	// Applying stops at the cap; checking does not.
	_, migrates := store.counts()
	if migrates != schemaMaxApplyAttempts {
		t.Errorf("Migrate called %d times, want the %d-attempt cap", migrates, schemaMaxApplyAttempts)
	}
}

// A failing Migrate must not retry in a tight loop: the whole attempt budget
// would be spent inside one bad second, so a ClickHouse hiccup — the transient
// case self-heal exists for — would permanently disable it.
func TestSchemaGateSpacesFailedApplies(t *testing.T) {
	store := &fakeSchemaStore{
		statuses:   []storage.SchemaStatus{notReady()},
		migrateErr: errors.New("transient"),
	}
	gate := newSchemaGate(modules.AllSet(), "otel", true)
	gate.recheck = time.Hour // one attempt, then park

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	captureLogs(t, func() { gate.run(ctx, store) })

	if _, migrates := store.counts(); migrates != 1 {
		t.Errorf("Migrate called %d times in one interval, want 1 (failed applies must wait)", migrates)
	}
}

func TestSchemaGateWaitHonorsContext(t *testing.T) {
	gate := newTestGate(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if gate.wait(ctx) {
		t.Error("wait returned true on a cancelled context")
	}
}

// TestSchemaGateSurvivesCheckErrors: a backend that errors the check must not
// be mistaken for an unmigrated one and must not trigger DDL.
func TestSchemaGateSurvivesCheckErrors(t *testing.T) {
	store := &fakeSchemaStore{
		statuses: []storage.SchemaStatus{{}, ready()},
		errs:     []error{errors.New("connection reset")},
	}
	gate := newTestGate(true)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	gate.run(ctx, store)

	if _, migrates := store.counts(); migrates != 0 {
		t.Errorf("Migrate called %d times after a check error, want 0", migrates)
	}
}

func countLevel(logs, level string) int {
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Level string `json:"level"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err == nil && rec.Level == level {
			n++
		}
	}
	return n
}
