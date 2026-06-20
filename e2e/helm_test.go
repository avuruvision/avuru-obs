//go:build e2ehelm

// Helm install smoke: asserts the chart serves a working OTLP backend.
// Driven by deploy/helm/e2e-helm.sh (kind up → helm install → seed → test).
// Assertions use ONLY the deterministic seeded fixture, via port-forwarded
// hub on localhost:8080.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const (
	helmHubURL    = "http://localhost:8080"
	helmSeedTrace = "aaaa1111bbbb2222cccc3333dddd4444"
)

func helmGetJSON(path string, out any) error {
	resp, err := http.Get(helmHubURL + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func TestSeededViaHelm(t *testing.T) {
	// The chart wired the hub to ClickHouse (migration hook + gateway insert).
	var status struct {
		ClickHouse string `json:"clickhouse"`
	}
	if err := helmGetJSON("/api/v1/status", &status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.ClickHouse != "ok" {
		t.Fatalf("clickhouse status = %q, want ok", status.ClickHouse)
	}

	// Seeded trace: exactly 3 spans (ingestion is async — poll).
	var trace struct {
		Spans []struct {
			Service string `json:"service"`
		} `json:"spans"`
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := helmGetJSON("/api/v1/traces/"+helmSeedTrace, &trace)
		if err == nil && len(trace.Spans) == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("seeded trace not 3 spans within 60s (err=%v, got %d)", err, len(trace.Spans))
		}
		time.Sleep(2 * time.Second)
	}

	// Correlated logs: the fixture has 2 logs on the seeded trace.
	var logs struct {
		Logs []struct {
			Severity string `json:"severity"`
		} `json:"logs"`
	}
	if err := helmGetJSON("/api/v1/traces/"+helmSeedTrace+"/logs", &logs); err != nil {
		t.Fatalf("trace logs: %v", err)
	}
	if len(logs.Logs) != 2 {
		t.Fatalf("got %d correlated logs, want 2", len(logs.Logs))
	}
}
