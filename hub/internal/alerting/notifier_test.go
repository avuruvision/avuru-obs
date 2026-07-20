package alerting

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// loopbackAllow lets tests reach httptest servers (which bind 127.0.0.1),
// mirroring the real "allowlist a private receiver" override.
func loopbackAllow(t *testing.T) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, cidr := range []string{"127.0.0.0/8", "::1/128"} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func testNote() Notification {
	return Notification{Rule: "r", Target: "group:payments", Kind: KindFired, Status: "down", Reason: "boom", Tenant: "prod-eu", At: time.Unix(0, 0).UTC()}
}

func TestWebhookSendSuccessAndSignature(t *testing.T) {
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Avuru-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(2*time.Second, 2, loopbackAllow(t))
	ch := Channel{Name: "ops", Type: "webhook", URL: srv.URL, Secret: "s3cr3t"}
	if err := n.Send(context.Background(), ch, testNote()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(string(gotBody), `"kind":"fired"`) || strings.Contains(string(gotBody), "s3cr3t") {
		t.Errorf("payload wrong or leaked secret: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"tenant":"prod-eu"`) {
		t.Errorf("payload missing tenant: %s", gotBody)
	}
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	mac.Write(gotBody)
	if gotSig != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Errorf("signature mismatch: %s", gotSig)
	}
}

func TestWebhookRetriesThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(2*time.Second, 2, loopbackAllow(t))
	if err := n.Send(context.Background(), Channel{Name: "ops", Type: "webhook", URL: srv.URL}, testNote()); err != nil {
		t.Fatalf("should succeed after one retry: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("want 2 calls, got %d", calls)
	}
}

func TestWebhookGivesUpAfterRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(2*time.Second, 1, loopbackAllow(t))
	if err := n.Send(context.Background(), Channel{Name: "ops", Type: "webhook", URL: srv.URL}, testNote()); err == nil {
		t.Fatal("want error after exhausting retries")
	}
}

func TestWebhook4xxIsTerminal(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(2*time.Second, 3, loopbackAllow(t))
	if err := n.Send(context.Background(), Channel{Name: "ops", Type: "webhook", URL: srv.URL}, testNote()); err == nil {
		t.Fatal("4xx should return an error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("4xx must not retry, got %d calls", calls)
	}
}

func TestWebhookSSRFBlocksPrivateAndMetadata(t *testing.T) {
	// No allowlist: a loopback httptest server must be refused at dial time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(time.Second, 0, nil)
	err := n.Send(context.Background(), Channel{Name: "x", Type: "webhook", URL: srv.URL}, testNote())
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("loopback target must be blocked, got %v", err)
	}

	// Cloud metadata IP is blocked without dialing (fast).
	err = n.Send(context.Background(), Channel{Name: "x", Type: "webhook", URL: "http://169.254.169.254/latest/meta-data"}, testNote())
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("metadata IP must be blocked, got %v", err)
	}
}

func TestWebhookAllowlistOverridesBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// With loopback allowed, the same target succeeds — proving the override.
	n := NewWebhookNotifier(2*time.Second, 0, loopbackAllow(t))
	if err := n.Send(context.Background(), Channel{Name: "x", Type: "webhook", URL: srv.URL}, testNote()); err != nil {
		t.Errorf("allowlisted loopback should succeed: %v", err)
	}
}
