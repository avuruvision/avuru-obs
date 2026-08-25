package health

import (
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func groupWithCheck(name string, tier Tier) Config {
	return Config{
		DefaultTier: TierT2,
		Groups: []Group{{
			Name: name, Tier: tier,
			Selector: Selector{Services: []string{"api"}},
			Checks:   []Check{{ID: "core-login", URL: "https://app.example.com/health"}},
		}},
	}
}

func report(name, status string, spans uint64) Report {
	return Report{
		Overall: status,
		Groups:  []GroupHealth{{Name: name, Status: status, SpanCount: spans}},
	}
}

func results(t *testing.T, oks ...bool) map[string][]storage.CheckResult {
	t.Helper()
	now := time.Now().UTC()
	var out []storage.CheckResult
	for i, ok := range oks { // newest first, as the store returns them
		out = append(out, storage.CheckResult{
			CheckID: "core-login", Group: "core", OK: ok, Status: 200,
			At: now.Add(-time.Duration(i) * time.Minute),
			Error: func() string {
				if ok {
					return ""
				}
				return "connection refused"
			}(),
		})
	}
	return map[string][]storage.CheckResult{"core-login": out}
}

// THE case this feature exists for. No traffic at 3 a.m. is indistinguishable
// from no service — unless something is calling the endpoint on purpose.
func TestSilentGroupWithPassingCheckIsHealthy(t *testing.T) {
	got := ApplyChecks(report("core", StatusIdle, 0), groupWithCheck("core", TierT0), results(t, true, true))
	if got.Groups[0].Status != StatusHealthy {
		t.Errorf("status = %q, want healthy — a passing probe is evidence of service", got.Groups[0].Status)
	}
	if got.Overall != StatusHealthy {
		t.Errorf("overall = %q, want healthy", got.Overall)
	}
}

// The other half: silent AND the probe is failing means nothing is serving.
func TestSilentGroupWithFailingCheckIsDown(t *testing.T) {
	got := ApplyChecks(report("core", StatusIdle, 0), groupWithCheck("core", TierT0), results(t, false, false))
	g := got.Groups[0]
	if g.Status != StatusDown {
		t.Errorf("status = %q, want down", g.Status)
	}
	if g.Reason == "" {
		t.Error("no reason on a check-driven status")
	}
	if len(g.Checks) != 1 || !g.Checks[0].Failing {
		t.Errorf("check state not reported as failing: %+v", g.Checks)
	}
}

// One failure is a blip: a restart, a lost lease, a dropped packet. A board
// that moves on one is a board people stop trusting.
func TestSingleFailureDoesNotMoveTheGroup(t *testing.T) {
	got := ApplyChecks(report("core", StatusIdle, 0), groupWithCheck("core", TierT0), results(t, false, true))
	g := got.Groups[0]
	if g.Status != StatusIdle {
		t.Errorf("status = %q, want idle — one failure is not evidence", g.Status)
	}
	if g.Checks[0].Failing {
		t.Error("a single failure was reported as failing")
	}
	if g.Checks[0].ConsecutiveFailures != 1 {
		t.Errorf("consecutive failures = %d, want 1", g.Checks[0].ConsecutiveFailures)
	}
}

// Traffic says something works, the probe says something does not. Degraded is
// the honest middle, and it must not overwrite a worse status the RED data
// already established.
func TestFailingCheckWithTrafficDegrades(t *testing.T) {
	got := ApplyChecks(report("core", StatusHealthy, 500), groupWithCheck("core", TierT1), results(t, false, false))
	if got.Groups[0].Status != StatusDegraded {
		t.Errorf("status = %q, want degraded", got.Groups[0].Status)
	}

	worse := ApplyChecks(report("core", StatusDown, 500), groupWithCheck("core", TierT1), results(t, false, false))
	if worse.Groups[0].Status != StatusDown {
		t.Errorf("status = %q, want the worse RED verdict kept", worse.Groups[0].Status)
	}
}

// Declared but never run: say nothing rather than invent a verdict.
func TestDeclaredButUnrunCheckLeavesTheGroupAlone(t *testing.T) {
	got := ApplyChecks(report("core", StatusIdle, 0), groupWithCheck("core", TierT0), nil)
	if got.Groups[0].Status != StatusIdle {
		t.Errorf("status = %q, want idle — nothing has been probed yet", got.Groups[0].Status)
	}
	if len(got.Groups[0].Checks) != 0 {
		t.Errorf("invented check state: %+v", got.Groups[0].Checks)
	}
}

// An install with no checks must behave exactly as it did before this existed.
func TestNoChecksIsInert(t *testing.T) {
	cfg := Config{DefaultTier: TierT2, Groups: []Group{{
		Name: "core", Tier: TierT1, Selector: Selector{Services: []string{"api"}},
	}}}
	in := report("core", StatusIdle, 0)
	got := ApplyChecks(in, cfg, results(t, false, false))
	if got.Groups[0].Status != StatusIdle || len(got.Groups[0].Checks) != 0 {
		t.Errorf("a group declaring no checks was changed: %+v", got.Groups[0])
	}
}
