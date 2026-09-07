package api

import (
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func obs(mtlsReq, plainReq, unknownReq, mtlsConn, plainConn uint64, callers ...storage.MeshCaller) *observed {
	return observe(storage.MeshWorkloadSecurity{
		Namespace: "shop", Workload: "checkout", Reporter: "destination",
		Counts: storage.MeshSecurityCounts{
			MTLSRequests: mtlsReq, PlaintextRequests: plainReq, UnknownRequests: unknownReq,
			MTLSConnections: mtlsConn, PlaintextConnections: plainConn,
		},
		PlaintextCallers: callers,
	})
}

// The whole verdict table, in one place. Every consumer reads this fold and
// none re-derives a verdict, so this is the test that decides what the
// security screen says.
func TestPostureVerdictTable(t *testing.T) {
	legacy := storage.MeshCaller{Namespace: "shop", Workload: "legacy-cron", Units: 12}
	for _, tc := range []struct {
		name    string
		in      postureInput
		verdict string
		code    meshconfig.Code
		sev     meshconfig.Severity
		message string // substring the finding's message must carry
	}{
		{
			name:    "strict and all mutual TLS",
			in:      postureInput{declared: true, declaredMode: "STRICT", obs: obs(100, 0, 0, 0, 0)},
			verdict: postureStrictAndMTLS,
		},
		{
			name:    "declared strict, observed plaintext — the finding this surface exists for",
			in:      postureInput{namespace: "shop", workload: "checkout", declared: true, declaredMode: "STRICT", obs: obs(100, 12, 0, 0, 3)},
			verdict: postureStrictButPlaintext,
			code:    meshconfig.CodeMTLSNotEnforced, sev: meshconfig.SeverityError,
			message: "12 plaintext requests and 3 plaintext connections reached shop/checkout under a STRICT policy",
		},
		{
			name:    "permissive, every caller already on mutual TLS",
			in:      postureInput{namespace: "shop", workload: "checkout", declared: true, declaredMode: "PERMISSIVE", obs: obs(50, 0, 0, 4, 0)},
			verdict: posturePermissiveAllMTLS,
			code:    meshconfig.CodeMTLSReadyToTighten, sev: meshconfig.SeverityInfo,
			message: "STRICT would refuse nothing",
		},
		{
			name:    "no policy read at all counts as permissive: the mesh default accepts plaintext",
			in:      postureInput{declared: true, declaredMode: "", obs: obs(50, 0, 0, 0, 0)},
			verdict: posturePermissiveAllMTLS,
			code:    meshconfig.CodeMTLSReadyToTighten, sev: meshconfig.SeverityInfo,
		},
		{
			name:    "permissive with plaintext callers, named",
			in:      postureInput{namespace: "shop", workload: "checkout", declared: true, declaredMode: "PERMISSIVE", obs: obs(50, 12, 0, 0, 0, legacy)},
			verdict: posturePermissivePlaintext,
			code:    meshconfig.CodePlaintextCallers, sev: meshconfig.SeverityWarning,
			message: "shop/legacy-cron (12)",
		},
		{
			name:    "disabled",
			in:      postureInput{declared: true, declaredMode: "DISABLE", obs: obs(0, 40, 0, 0, 0)},
			verdict: postureDisabled,
		},
		{
			name:    "ambient namespace, traced traffic, no proxy reported it",
			in:      postureInput{namespace: "shop", workload: "checkout", declared: true, dataplane: "ambient", hasTraffic: true},
			verdict: postureUncarried,
			code:    meshconfig.CodeTrafficUncarried, sev: meshconfig.SeverityError,
			message: "no proxy reported carrying it",
		},
		{
			name:    "sidecar namespace with traffic and no observation is not a verdict",
			in:      postureInput{declared: true, dataplane: "sidecar", hasTraffic: true},
			verdict: postureUnknown,
		},
		{
			name:    "ambient namespace, no traffic, no observation: nothing to say",
			in:      postureInput{declared: true, dataplane: "ambient", hasTraffic: false},
			verdict: postureUnknown,
		},
		{
			name:    "configuration not read: observed only, mutual TLS",
			in:      postureInput{declared: false, obs: obs(10, 0, 0, 0, 0)},
			verdict: postureObservedOnlyMTLS,
		},
		{
			name:    "configuration not read: observed only, plaintext",
			in:      postureInput{declared: false, obs: obs(10, 1, 0, 0, 0)},
			verdict: postureObservedOnlyPlain,
		},
		{
			name:    "configuration not read and ambient traffic uncarried is NOT claimed",
			in:      postureInput{declared: false, dataplane: "ambient", hasTraffic: true},
			verdict: postureUnknown,
		},
		{
			name:    "observed and idle",
			in:      postureInput{declared: true, declaredMode: "STRICT", obs: obs(0, 0, 0, 0, 0)},
			verdict: postureIdle,
		},
		{
			name:    "only unknown-policy traffic: the proxy declined to say, so do we",
			in:      postureInput{declared: true, declaredMode: "STRICT", obs: obs(0, 0, 7, 0, 0)},
			verdict: postureUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verdict, finding := posture(tc.in)
			if verdict != tc.verdict {
				t.Fatalf("verdict = %q, want %q", verdict, tc.verdict)
			}
			if tc.code == "" {
				if finding != nil {
					t.Fatalf("unexpected finding %+v", *finding)
				}
				return
			}
			if finding == nil {
				t.Fatalf("no finding, want %s", tc.code)
			}
			if finding.Code != tc.code || finding.Severity != tc.sev {
				t.Errorf("finding = %s/%s, want %s/%s", finding.Code, finding.Severity, tc.code, tc.sev)
			}
			if finding.Hint == "" {
				t.Error("a finding without a hint sends the reader looking")
			}
			if finding.Ref != tc.in.namespace+"/"+tc.in.workload {
				t.Errorf("ref = %q, want ns/workload", finding.Ref)
			}
			if !strings.Contains(finding.Message, tc.message) {
				t.Errorf("message %q does not carry %q", finding.Message, tc.message)
			}
		})
	}
}

// Five callers named, the rest counted: a finding that lists forty callers is
// not a finding anyone reads.
func TestCallersPhraseBoundsTheList(t *testing.T) {
	var callers []storage.MeshCaller
	for i := 0; i < 8; i++ {
		callers = append(callers, storage.MeshCaller{Namespace: "shop", Workload: "c" + string(rune('0'+i)), Units: uint64(8 - i)})
	}
	got := callersPhrase(callers)
	if !strings.HasPrefix(got, "shop/c0 (8), ") || !strings.HasSuffix(got, "and 3 more") {
		t.Errorf("phrase = %q", got)
	}
	if strings.Contains(got, "c5") {
		t.Errorf("a sixth caller was named: %q", got)
	}
}

// The share leaves unknown out of both sides and refuses to divide by zero.
func TestObservedMTLSShare(t *testing.T) {
	if s := obs(0, 0, 9, 0, 0).mtlsShare(); s != nil {
		t.Errorf("share of nothing classified = %v, want nil", *s)
	}
	if s := obs(3, 1, 50, 0, 0).mtlsShare(); s == nil || *s != 0.75 {
		t.Errorf("share = %v, want 0.75 with unknown excluded", s)
	}
}

// The adapter reports what the snapshot knows and nothing more: an empty mode
// is "no policy read", with no scope invented for it, and Declared follows the
// read's own state.
func TestSnapshotDeclaredAdapter(t *testing.T) {
	d := newSnapshotDeclared(meshconfig.Snapshot{
		State: meshconfig.StateOK,
		Namespaces: []meshconfig.Namespace{
			{Name: "shop", DataplaneMode: "ambient", MTLSMode: "STRICT"},
			{Name: "legacy", DataplaneMode: "sidecar"},
		},
	})
	if !d.Declared() {
		t.Fatal("an OK snapshot is not declared")
	}
	if mode, scope := d.EffectiveMTLS("shop", "checkout"); mode != "STRICT" || scope != "namespace" {
		t.Errorf("shop = %s/%s", mode, scope)
	}
	if mode, scope := d.EffectiveMTLS("legacy", "x"); mode != "" || scope != "" {
		t.Errorf("legacy = %q/%q, want no policy and no scope", mode, scope)
	}
	if d.DataplaneMode("legacy") != "sidecar" || d.DataplaneMode("nowhere") != "" {
		t.Error("dataplane modes mis-resolved")
	}
	if newSnapshotDeclared(meshconfig.NoopReader{}.Snapshot(t.Context())).Declared() {
		t.Error("a NoopReader snapshot reported declared")
	}
}
