package alerting

import (
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/health"
)

// Status values for a rule×target in the state machine.
const (
	stateOK      = "ok"
	statePending = "pending"
	stateFiring  = "firing"
)

// Kind of a notification.
const (
	KindFired    = "fired"
	KindResolved = "resolved"
)

// StateKey identifies one rule×target.
type StateKey struct {
	Rule   string
	Target string
}

// TargetState is the durable per-key state.
type TargetState struct {
	Status         string // ok | pending | firing
	Since          time.Time
	LastNotifiedAt time.Time
}

// State is the evaluator's memory across ticks.
type State map[StateKey]TargetState

// Notification is a delivery to make this tick.
type Notification struct {
	Rule    string
	Target  string
	Kind    string // fired | resolved
	Status  string // health status at the time
	Reason  string
	Tenant  string // stamped by the runner (Evaluate itself is tenant-agnostic)
	At      time.Time
	Channel string
}

// targetInfo is a flattened health target.
type targetInfo struct {
	status string
	reason string
}

// Evaluate is the pure alerting state machine: given the config, the current
// health report, the previous state and the current time, it returns the next
// state and the notifications to deliver. It never recurses and reads only the
// current report, so it always terminates. Idle/unknown targets never fire.
func Evaluate(cfg Config, report health.Report, prev State, now time.Time) (State, []Notification) {
	targets := buildTargets(report)
	next := State{}
	var notes []Notification

	for _, rule := range cfg.Rules {
		for _, target := range candidateTargets(rule, targets, prev) {
			info, present := targets[target]
			match := present && selectorMatches(rule.Selector, target) && conditionMatches(rule.When, info.status)
			prevSt := prev[StateKey{rule.Name, target}]

			switch {
			case match && prevSt.Status != statePending && prevSt.Status != stateFiring:
				// ok/absent -> pending: start the `for` timer.
				next[StateKey{rule.Name, target}] = TargetState{Status: statePending, Since: now}
			case match && prevSt.Status == statePending:
				if now.Sub(prevSt.Since) >= rule.For.Std() {
					next[StateKey{rule.Name, target}] = TargetState{Status: stateFiring, Since: prevSt.Since, LastNotifiedAt: now}
					notes = append(notes, fire(rule, target, info, now))
				} else {
					next[StateKey{rule.Name, target}] = prevSt // keep waiting
				}
			case match && prevSt.Status == stateFiring:
				next[StateKey{rule.Name, target}] = prevSt // already firing — dedup, no emit
			case !match && prevSt.Status == stateFiring:
				notes = append(notes, resolve(rule, target, info, present, now))
				// drop key -> back to ok
			case !match && prevSt.Status == statePending:
				// never fired -> drop key silently
			}
		}
	}
	return next, notes
}

func fire(rule Rule, target string, info targetInfo, now time.Time) Notification {
	return Notification{
		Rule: rule.Name, Target: target, Kind: KindFired,
		Status: info.status, Reason: info.reason, At: now, Channel: rule.Channel,
	}
}

func resolve(rule Rule, target string, info targetInfo, present bool, now time.Time) Notification {
	status, reason := info.status, info.reason
	if !present {
		status, reason = health.StatusUnknown, "target no longer reporting"
	}
	return Notification{
		Rule: rule.Name, Target: target, Kind: KindResolved,
		Status: status, Reason: reason, At: now, Channel: rule.Channel,
	}
}

// candidateTargets is the set of targets to evaluate for a rule: every present
// target the selector matches, plus every target the rule already has state for
// (so a firing alert resolves when its target recovers or vanishes).
func candidateTargets(rule Rule, targets map[string]targetInfo, prev State) []string {
	set := map[string]bool{}
	for t := range targets {
		if selectorMatches(rule.Selector, t) {
			set[t] = true
		}
	}
	for k := range prev {
		if k.Rule == rule.Name {
			set[k.Target] = true
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	return out
}

// buildTargets flattens a health report into addressable targets: one per
// group, one per service (member), and one synthetic per tier (worst-of that
// tier's groups).
func buildTargets(report health.Report) map[string]targetInfo {
	out := map[string]targetInfo{}
	tierWorst := map[string]targetInfo{}
	for _, g := range report.Groups {
		out["group:"+g.Name] = targetInfo{g.Status, g.Reason}
		for _, m := range g.Members {
			out["service:"+m.Service] = targetInfo{m.EffectiveStatus, m.Reason}
		}
		if g.Tier != "" {
			tk := string(g.Tier)
			cur, ok := tierWorst[tk]
			if !ok || severity(g.Status) > severity(cur.status) {
				tierWorst[tk] = targetInfo{g.Status, g.Name + " is " + g.Status}
			}
		}
	}
	for tier, info := range tierWorst {
		out["tier:"+tier] = info
	}
	return out
}

func selectorMatches(sel Selector, target string) bool {
	switch {
	case strings.HasPrefix(target, "group:"):
		return contains(sel.Groups, strings.TrimPrefix(target, "group:"))
	case strings.HasPrefix(target, "service:"):
		return contains(sel.Services, strings.TrimPrefix(target, "service:"))
	case strings.HasPrefix(target, "tier:"):
		return contains(sel.Tiers, strings.TrimPrefix(target, "tier:"))
	default:
		return false
	}
}

func conditionMatches(when, status string) bool {
	switch when {
	case WhenDown:
		return status == health.StatusDown
	case WhenDegraded:
		return status == health.StatusDegraded
	case WhenNotHealthy:
		return status == health.StatusDown || status == health.StatusDegraded
	default:
		return false
	}
}

// severity ranks statuses for the tier worst-of. idle/unknown sit at 0.
func severity(status string) int {
	switch status {
	case health.StatusHealthy:
		return 1
	case health.StatusDegraded:
		return 2
	case health.StatusDown:
		return 3
	default:
		return 0
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
