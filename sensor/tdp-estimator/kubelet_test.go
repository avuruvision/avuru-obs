package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

const samplePodList = `{
  "items": [
    {"metadata": {"uid": "abc-123", "name": "web-1", "namespace": "shop"}},
    {"metadata": {"uid": "def-456", "name": "cart-1", "namespace": "shop"}}
  ]
}`

func TestFetchPods(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pods" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer test-token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(samplePodList))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	pods, err := fetchPods(client, srv.URL, "test-token")
	if err != nil {
		t.Fatalf("fetchPods: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("len(pods) = %d, want 2", len(pods))
	}
	if pods["abc-123"] != (podIdentity{Name: "web-1", Namespace: "shop"}) {
		t.Errorf("pods[abc-123] = %+v, want {web-1 shop}", pods["abc-123"])
	}
	if pods["def-456"] != (podIdentity{Name: "cart-1", Namespace: "shop"}) {
		t.Errorf("pods[def-456] = %+v, want {cart-1 shop}", pods["def-456"])
	}
}
