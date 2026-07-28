package green

import (
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
)

// BudgetRulePrefix namespaces every budget rule key ("green:<budget>:warn|:over")
// so the alerting tick can recognise green-owned rows in the shared alert_state
// — e.g. to preserve them when a transient usage recompute fails, instead of
// letting diffToSave clobber a firing budget to ok. Exported for that caller;
// the reverse import (alerting → green) never happens.
const BudgetRulePrefix = "green:"

const (
	warnSuffix = ":warn"
	overSuffix = ":over"

	// stateFiring mirrors the alerting evaluator's private "firing" enum value.
	// Budgets go straight ok→firing (no pending step), and internal/alerting
	// stays unedited by contract, so the value is duplicated as a local literal
	// rather than exported from there.
	stateFiring = "firing"
)

// Budget status vocabulary — the human-facing status a budget reports: written
// to alert_history by the tick (Notification.Status) and to the budgets DTO by
// the API endpoint. One exported source so history and UI never disagree. (The
// durable alert_state enum stays ok/pending/firing; these are a separate, wider
// field.)
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusOver = "exceeded"
)

// warnRuleName and overRuleName build a budget's two rule keys. Same-package
// only (the tick composes keys from BudgetRulePrefix, not these).
func warnRuleName(budget string) string { return BudgetRulePrefix + budget + warnSuffix }
func overRuleName(budget string) string { return BudgetRulePrefix + budget + overSuffix }

// EvaluateBudgets is the pure budget state machine: given the green config, the
// month-to-date used kgCO2e per group, the previous alerting state and the
// current time, it returns the next state (green-owned keys only) and the
// notifications to deliver this tick. Each budget yields two logical rules —
// green:<name>:warn (used ≥ WarnRatio × budget) and green:<name>:over (used ≥
// budget), both Target=group:<group> — using only the existing ok/firing states
// (no new enum, no migration). A rule fires once on the ok→firing crossing,
// dedups while firing, and resolves (firing→ok) on drop-back or month rollover
// (the tick recomputes month-to-date usage, which resets; this evaluator just
// sees the lower number). Pure: no I/O, now injected. Green imports alerting
// types; never the reverse.
func EvaluateBudgets(cfg Config, usedKgByGroup map[string]float64, prev alerting.State, now time.Time) (alerting.State, []alerting.Notification) {
	next := alerting.State{}
	var notes []alerting.Notification
	for _, b := range cfg.Budgets {
		if b.MonthlyKgCO2e <= 0 {
			// Validate rejects this; the guard keeps the ratio finite for
			// direct-constructed configs (tests, defensive).
			continue
		}
		warn := b.WarnRatio
		if warn <= 0 {
			warn = DefaultWarnRatio
		}
		used := usedKgByGroup[b.Group]
		ratio := used / b.MonthlyKgCO2e
		target := "group:" + b.Group
		reason := budgetReason(b, used, ratio)
		evalBudgetRule(next, &notes, prev, warnRuleName(b.Name), target, b.Channel, ratio >= warn, StatusWarn, reason, now)
		evalBudgetRule(next, &notes, prev, overRuleName(b.Name), target, b.Channel, ratio >= 1.0, StatusOver, reason, now)
	}
	return next, notes
}

// evalBudgetRule applies one rule's ok↔firing transition, mirroring the alerting
// evaluator minus the pending step. A crossed rule fires once then dedups; an
// un-crossed rule that was firing resolves and is dropped from next (so the
// tick's diffToSave supersedes the stale row with an explicit ok); an un-crossed
// rule that was not firing adds no key at all.
func evalBudgetRule(next alerting.State, notes *[]alerting.Notification, prev alerting.State, rule, target, channel string, crossed bool, firingStatus, reason string, now time.Time) {
	key := alerting.StateKey{Rule: rule, Target: target}
	prevSt := prev[key]
	switch {
	case crossed && prevSt.Status == stateFiring:
		next[key] = prevSt // already firing — dedup, no emit
	case crossed:
		next[key] = alerting.TargetState{Status: stateFiring, Since: now, LastNotifiedAt: now}
		*notes = append(*notes, alerting.Notification{
			Rule: rule, Target: target, Kind: alerting.KindFired,
			Status: firingStatus, Reason: reason, At: now, Channel: channel,
		})
	case prevSt.Status == stateFiring:
		*notes = append(*notes, alerting.Notification{
			Rule: rule, Target: target, Kind: alerting.KindResolved,
			Status: StatusOK, Reason: reason, At: now, Channel: channel,
		})
		// drop key → diffToSave writes the explicit ok row
	}
}

// budgetReason is the human line recorded in the alert history for a crossing,
// status-independent so both the fire and the resolve of a budget read the same
// usage snapshot.
func budgetReason(b Budget, used, ratio float64) string {
	return fmt.Sprintf("budget %q for group %q at %.0f%% (%.4g of %.4g kgCO2e)", b.Name, b.Group, ratio*100, used, b.MonthlyKgCO2e)
}
