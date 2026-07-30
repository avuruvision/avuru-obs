//go:build e2e

// Per-project ingest keys (auth Plan C) end to end against the compose stack.
//
// OPT-IN: needs the ingest-e2e overlay (two gateways, one enforce, one log) and
// runs only when AVURUOBS_E2E_INGEST=1, so the default `make e2e` run is
// unaffected. See deploy/compose/docker-compose.ingest-e2e.yaml for the boot
// command.
//
// Three properties, in order of how much they matter:
//
//  1. enforce REJECTS unkeyed OTLP — the credential is actually required.
//  2. enforce OVERRIDES a client-claimed avuru.tenant with the key's project —
//     this is the whole point of the feature. Topology trust is replaced by a
//     credential, so a sender that lies about its tenant lands in the tenant its
//     key says, not the one it asked for.
//  3. log NEVER drops an unkeyed sender — the drop-in promise survives upgrade.
package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// Fresh IDs per test so a re-run never collides with telemetry left by the
// previous one (the stack is not torn down between -count=1 invocations).
func randTraceID(t *testing.T) string { return randHex(t, 16) }
func randSpanID(t *testing.T) string  { return randHex(t, 8) }

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

const (
	// Seeded by the overlay for project "payments".
	seededIngestKey = "avuruk_e2e0000000000000000000000000000"
	// The gateway config upserts avuru.tenant=default BEFORE tenantfromauth, and
	// the payload below claims a third value — so a pass can only happen if the
	// key's project genuinely won over both.
	claimedTenant = "attacker-claimed"
	keyProject    = "payments"
)

var (
	enforceOTLP = envOr("AVURUOBS_E2E_OTLP_ENFORCE", "http://localhost:4318")
	logOTLP     = envOr("AVURUOBS_E2E_OTLP_LOG", "http://localhost:4328")
)

func requireIngestE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("AVURUOBS_E2E_INGEST") != "1" {
		t.Skip("ingest-key e2e needs the ingest-e2e overlay; set AVURUOBS_E2E_INGEST=1")
	}
}

// otlpTraceJSON builds a minimal OTLP/HTTP trace payload that CLAIMS a tenant.
// The claim is the point: it must be overridden in enforce mode.
func otlpTraceJSON(traceID, spanID, tenant string) []byte {
	now := time.Now().UnixNano()
	return []byte(fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[
      {"key":"service.name","value":{"stringValue":"ingest-e2e"}},
      {"key":"avuru.tenant","value":{"stringValue":%q}}]},
      "scopeSpans":[{"spans":[{"traceId":%q,"spanId":%q,"name":"ingest-e2e",
      "kind":1,"startTimeUnixNano":"%d","endTimeUnixNano":"%d"}]}]}]}`,
		tenant, traceID, spanID, now, now+1_000_000))
}

// postOTLP sends one trace to an OTLP/HTTP endpoint, optionally with a key, and
// returns the HTTP status.
func postOTLP(t *testing.T, base, traceID, spanID, tenant, key string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/traces",
		bytes.NewReader(otlpTraceJSON(traceID, spanID, tenant)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// Deliberately NOT apiClient: this is a telemetry sender, not a hub API
	// caller, and it must carry only the credential under test.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s/v1/traces: %v", base, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestIngestKeyEnforceRejectsUnkeyedOTLP(t *testing.T) {
	requireIngestE2E(t)

	code := postOTLP(t, enforceOTLP, randTraceID(t), randSpanID(t), claimedTenant, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("unkeyed export to an enforcing gateway returned %d, want 401 — "+
			"the key is not actually required", code)
	}

	// A wrong key must fail the same way (not just a missing header).
	code = postOTLP(t, enforceOTLP, randTraceID(t), randSpanID(t), claimedTenant, "avuruk_wrong")
	if code != http.StatusUnauthorized {
		t.Fatalf("bogus key returned %d, want 401", code)
	}
}

func TestIngestKeyStampsTenantFromKeyProject(t *testing.T) {
	requireIngestE2E(t)

	traceID, spanID := randTraceID(t), randSpanID(t)
	if code := postOTLP(t, enforceOTLP, traceID, spanID, claimedTenant, seededIngestKey); code != http.StatusOK {
		t.Fatalf("keyed export returned %d, want 200", code)
	}

	// It lands under the KEY's project...
	poll(t, 60*time.Second, func() error {
		code, err := getJSONTenant("/api/v1/traces/"+traceID, keyProject, nil)
		if code != http.StatusOK {
			return fmt.Errorf("trace not visible under %q yet: code=%d err=%v", keyProject, code, err)
		}
		return nil
	})

	// ...and NOT under the tenant the sender claimed, nor the gateway's static
	// default. Either would mean the credential did not become the authority.
	for _, wrong := range []string{claimedTenant, "default"} {
		if code, _ := getJSONTenant("/api/v1/traces/"+traceID, wrong, nil); code == http.StatusOK {
			t.Fatalf("trace also visible under tenant %q — the key's project did not win", wrong)
		}
	}
}

func TestIngestKeyLogModeNeverDropsUnkeyedSenders(t *testing.T) {
	requireIngestE2E(t)

	// The drop-in promise: same unkeyed payload the enforcing gateway rejected.
	traceID, spanID := randTraceID(t), randSpanID(t)
	if code := postOTLP(t, logOTLP, traceID, spanID, claimedTenant, ""); code != http.StatusOK {
		t.Fatalf("log mode returned %d for an unkeyed sender, want 200 — "+
			"upgrading to ingest keys would have broken existing senders", code)
	}

	// And it is routed by the pre-existing tenant processors, untouched: the
	// gateway's resource/tenant upsert still wins over the client's claim, which
	// is exactly what happened before ingest keys existed.
	poll(t, 60*time.Second, func() error {
		code, err := getJSONTenant("/api/v1/traces/"+traceID, "default", nil)
		if code != http.StatusOK {
			return fmt.Errorf("unkeyed trace not visible under %q yet: code=%d err=%v", "default", code, err)
		}
		return nil
	})

	// Nothing rerouted it to the key's project — log mode wires no
	// tenantfromauth, so an unkeyed sender cannot land in a keyed project.
	if code, _ := getJSONTenant("/api/v1/traces/"+traceID, keyProject, nil); code == http.StatusOK {
		t.Fatalf("unkeyed trace leaked into the keyed project %q", keyProject)
	}
}
