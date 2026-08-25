package health

import (
	"fmt"
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// consecutiveFailuresToDegrade is how many probes in a row must fail before a
// group's status moves.
//
// One is too few. A single failed probe is a network blip, a rolling restart,
// or a pod that lost its lease — and a health board that reacts to one is a
// board people stop reading. Two consecutive failures across the check's own
// interval is the cheapest evidence that something is actually wrong.
const consecutiveFailuresToDegrade = 2

// CheckState is one check's current standing, for the group it answers for.
type CheckState struct {
	ID string `json:"id"`
	// OK is the latest result. Failing is the STATUS-bearing field — a check can
	// have failed once (OK false) without the group moving.
	OK                  bool    `json:"ok"`
	Failing             bool    `json:"failing"`
	ConsecutiveFailures int     `json:"consecutiveFailures"`
	LatencyMs           float64 `json:"latencyMs,omitempty"`
	Status              int     `json:"status,omitempty"`
	Error               string  `json:"error,omitempty"`
	LastRunISO          string  `json:"lastRun,omitempty"`
	// TraceID is the probe's own span: the click-through from "this check is
	// failing" to "here is the request that failed".
	TraceID string `json:"traceId,omitempty"`
}

// ApplyChecks folds probe outcomes into a rolled-up report.
//
// It runs AFTER Rollup rather than inside it so Rollup stays pure — no I/O, no
// clock — and so the check rules live in one readable place instead of being
// threaded through the RED path.
//
// The rules, and why:
//
//   - A group whose checks are all passing and which saw NO traffic reports
//     HEALTHY rather than idle. That is the whole feature: idle and dead look
//     identical in observed traffic, and a probe answering is the evidence that
//     tells them apart.
//   - A group with a failing check and no traffic is DOWN. Nobody else is
//     calling it, and the one thing that did call it did not get served.
//   - A group with a failing check and live traffic is at least DEGRADED — the
//     traffic says something works, the probe says something does not.
//   - Nothing moves on a single failure (see consecutiveFailuresToDegrade).
//
// A group with no checks is returned untouched, so an install that declares
// none gets byte-identical behaviour.
func ApplyChecks(report Report, cfg Config, states map[string][]storage.CheckResult) Report {
	byGroup := map[string][]Check{}
	for _, g := range cfg.Groups {
		if len(g.Checks) > 0 {
			byGroup[g.Name] = g.Checks
		}
	}
	if len(byGroup) == 0 {
		return report
	}

	for i := range report.Groups {
		g := &report.Groups[i]
		declared := byGroup[g.Name]
		if len(declared) == 0 {
			continue
		}
		g.Checks = checkStates(declared, states)
		if len(g.Checks) == 0 {
			// Declared but never run yet: say nothing rather than claim
			// something. The group keeps whatever its traffic reported.
			continue
		}

		failing, reason := firstFailing(g.Checks)
		quiet := g.SpanCount == 0
		switch {
		case failing:
			if quiet {
				g.Status, g.Reason = StatusDown, reason
			} else {
				g.Status = worst(g.Status, StatusDegraded)
				g.Reason = reason
			}
		case quiet && allLatestOK(g.Checks):
			// Serving, with nobody calling. The honest answer is healthy, not
			// idle — and it is the answer only a probe can give.
			//
			// `allLatestOK`, not merely "not failing": a check whose LAST probe
			// failed is below the two-in-a-row threshold and so must not move
			// the group DOWN, but it is equally no basis for calling it up.
			// Under that threshold the group keeps whatever it already said.
			g.Status = StatusHealthy
			g.Reason = "no traffic, but endpoint checks are passing"
		}
	}
	report.Overall = overallStatus(report.Groups)
	return report
}

// checkStates turns raw results into per-check standings, in declaration order
// so a group's checks read the way they were written.
func checkStates(declared []Check, states map[string][]storage.CheckResult) []CheckState {
	var out []CheckState
	for _, ck := range declared {
		results := states[ck.ID]
		if len(results) == 0 {
			continue
		}
		// Results arrive newest-first; count the leading run of failures.
		consecutive := 0
		for _, r := range results {
			if r.OK {
				break
			}
			consecutive++
		}
		latest := results[0]
		out = append(out, CheckState{
			ID:                  ck.ID,
			OK:                  latest.OK,
			Failing:             consecutive >= consecutiveFailuresToDegrade,
			ConsecutiveFailures: consecutive,
			LatencyMs:           latest.LatencyMs,
			Status:              latest.Status,
			Error:               latest.Error,
			LastRunISO:          latest.At.UTC().Format("2006-01-02T15:04:05Z07:00"),
			TraceID:             latest.TraceID,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func firstFailing(states []CheckState) (bool, string) {
	for _, s := range states {
		if !s.Failing {
			continue
		}
		detail := s.Error
		if detail == "" {
			detail = fmt.Sprintf("status %d", s.Status)
		}
		return true, fmt.Sprintf("check %q failing (%d in a row): %s", s.ID, s.ConsecutiveFailures, detail)
	}
	return false, ""
}

// allLatestOK reports whether every check's most recent probe passed. An empty
// list is false: nothing has answered, so there is nothing to promote on.
func allLatestOK(states []CheckState) bool {
	if len(states) == 0 {
		return false
	}
	for _, s := range states {
		if !s.OK {
			return false
		}
	}
	return true
}
