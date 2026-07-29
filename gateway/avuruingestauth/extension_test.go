package avuruingestauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/extension"
	"go.uber.org/zap"
)

// fakeHub answers the validate endpoint: key "good" → payments, else invalid.
// It counts calls so cache behavior can be asserted.
func fakeHub(t *testing.T, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			atomic.AddInt32(calls, 1)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sekret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body validateRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Key == "good" {
			io.WriteString(w, `{"valid":true,"project":"payments"}`)
			return
		}
		io.WriteString(w, `{"valid":false}`)
	}))
}

func newTestExt(t *testing.T, hubURL, mode string) *avuruIngestAuth {
	t.Helper()
	cfg := &Config{
		HubValidateURL: hubURL,
		InternalToken:  "sekret",
		Mode:           mode,
		CacheTTL:       30 * time.Second,
		StaleGrace:     5 * time.Minute,
		Timeout:        2 * time.Second,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	ext, err := newExtension(cfg, extension.Settings{})
	if err != nil {
		t.Fatalf("newExtension: %v", err)
	}
	ext.logger = zap.NewNop()
	return ext
}

// bearer builds a headers source map with an Authorization bearer token, using
// the canonical HTTP header key (what confighttp passes).
func bearer(key string) map[string][]string {
	return map[string][]string{"Authorization": {"Bearer " + key}}
}

func TestAuthenticateModes(t *testing.T) {
	hub := fakeHub(t, nil)
	defer hub.Close()
	ctx := context.Background()

	t.Run("enforce accepts good key and attaches project", func(t *testing.T) {
		ext := newTestExt(t, hub.URL, ModeEnforce)
		out, err := ext.Authenticate(ctx, bearer("good"))
		if err != nil {
			t.Fatalf("enforce rejected a good key: %v", err)
		}
		if got := client.FromContext(out).Auth.GetAttribute("project"); got != "payments" {
			t.Fatalf("project attr = %v, want payments", got)
		}
	})

	t.Run("enforce rejects a bad key", func(t *testing.T) {
		ext := newTestExt(t, hub.URL, ModeEnforce)
		if _, err := ext.Authenticate(ctx, bearer("bad")); err == nil {
			t.Fatal("enforce accepted a bad key")
		}
	})

	t.Run("enforce rejects a missing key", func(t *testing.T) {
		ext := newTestExt(t, hub.URL, ModeEnforce)
		if _, err := ext.Authenticate(ctx, map[string][]string{}); err == nil {
			t.Fatal("enforce accepted a missing key")
		}
	})

	t.Run("log accepts a bad key without attaching a project", func(t *testing.T) {
		ext := newTestExt(t, hub.URL, ModeLog)
		out, err := ext.Authenticate(ctx, bearer("bad"))
		if err != nil {
			t.Fatalf("log mode rejected: %v", err)
		}
		if client.FromContext(out).Auth != nil {
			t.Fatal("log mode attached auth data (must stay drop-in identical)")
		}
	})

	t.Run("log accepts a good key but still attaches nothing", func(t *testing.T) {
		ext := newTestExt(t, hub.URL, ModeLog)
		out, err := ext.Authenticate(ctx, bearer("good"))
		if err != nil {
			t.Fatalf("log mode rejected a good key: %v", err)
		}
		if client.FromContext(out).Auth != nil {
			t.Fatal("log mode must not stamp tenant even for valid keys")
		}
	})

	t.Run("off passes everything through untouched", func(t *testing.T) {
		ext := newTestExt(t, hub.URL, ModeOff)
		out, err := ext.Authenticate(ctx, map[string][]string{})
		if err != nil || client.FromContext(out).Auth != nil {
			t.Fatalf("off mode not a pass-through: err=%v", err)
		}
	})
}

func TestVerdictCaches(t *testing.T) {
	var calls int32
	hub := fakeHub(t, &calls)
	defer hub.Close()
	ext := newTestExt(t, hub.URL, ModeEnforce)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := ext.Authenticate(ctx, bearer("good")); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("hub called %d times, want 1 (positive cache)", n)
	}
}

func TestStaleGraceServesThroughHubOutage(t *testing.T) {
	var calls int32
	hub := fakeHub(t, &calls)
	ext := newTestExt(t, hub.URL, ModeEnforce)
	ctx := context.Background()

	// Prime the cache with a good verdict.
	if _, err := ext.Authenticate(ctx, bearer("good")); err != nil {
		t.Fatalf("prime: %v", err)
	}
	// Hub goes down; age the cached entry past CacheTTL but within StaleGrace.
	hub.Close()
	base := ext.now()
	ext.now = func() time.Time { return base.Add(time.Minute) } // > 30s TTL, < 5m grace

	out, err := ext.Authenticate(ctx, bearer("good"))
	if err != nil {
		t.Fatalf("stale grace did not serve through outage: %v", err)
	}
	if got := client.FromContext(out).Auth.GetAttribute("project"); got != "payments" {
		t.Fatalf("stale project = %v, want payments", got)
	}

	// Past the grace window, enforce fails closed.
	ext.now = func() time.Time { return base.Add(10 * time.Minute) }
	if _, err := ext.Authenticate(ctx, bearer("good")); err == nil {
		t.Fatal("enforce did not fail closed past stale grace")
	}
}
