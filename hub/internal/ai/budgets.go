package ai

import (
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
)

// BudgetRulePrefix namespaces every AI budget rule key ("ai:<budget>:warn|:over")
// so the alerting tick can recognise AI-owned rows in the shared alert_state —
// to preserve them when a transient usage recompute fails, instead of letting
// diffToSave clobber a firing budget to ok. Exported for that caller; the
// reverse import (alerting → ai) never happens.
//
// This is green's contract, kept deliberately: two state machines writing one
// alert_state table with different transition rules is how firing and pending
// semantics drift apart, so this file is green's machine with the unit changed
// rather than a second engine that happens to agree today.
const BudgetRulePrefix = "ai:"

const (
	warnSuffix = ":warn"
	overSuffix = ":over"

	// stateFiring mirrors the alerting evaluator's private "firing" enum value.
	// Budgets go straight ok→firing (no pending step), and internal/alerting
	// stays unedited by contract, so the value is duplicated as a local literal
	// rather than exported from there.
	stateFiring = "firing"
)

// Budget status vocabulary — the human-facing status a budget reports. Same
// three words green uses, so an operator reading alert history does not have to
// learn a second set. (The durable alert_state enum stays ok/pending/firing;
// these are a separate, wider field.)
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusOver = "exceeded"
)

func warnRuleName(budget string) string { return BudgetRulePrefix + budget + warnSuffix }
func overRuleName(budget string) string { return BudgetRulePrefix + budget + overSuffix }

// BudgetUsage is month-to-date spend, per calling service and for the estate.
//
// Tokens and cost travel together because a config may hold budgets of both
// kinds and one query answers for both. Unpriced counts what could not be
// priced, so a cost budget can say its number is a floor rather than reporting
// a total that quietly excludes half the traffic.
type BudgetUsage struct {
	// TokensByService and CostByService are keyed by calling service; the
	// Estate fields cover every service, including any past a query limit.
	TokensByService map[string]int64
	CostByService   map[string]float64
	EstateTokens    int64
	EstateCost      float64

	// UnpricedCallsByService counts calls whose model had no declared price,
	// and UnpricedEstateCalls the same across the estate. A cost budget
	// measured over these is measuring against a floor.
	UnpricedCallsByService map[string]uint64
	UnpricedEstateCalls    uint64

	// KnownServices is every calling service seen this month, so the tick can
	// warn about a budget scoped to a name that never appears.
	KnownServices map[string]bool
}

// Used returns the month-to-date spend for one budget in its own unit, and
// whether the number is a floor because some calls in scope could not be priced.
func (u BudgetUsage) Used(b Budget) (used float64, partial bool) {
	if b.Scope == "" {
		if b.IsCost() {
			return u.EstateCost, u.UnpricedEstateCalls > 0
		}
		return float64(u.EstateTokens), false
	}
	if b.IsCost() {
		return u.CostByService[b.Scope], u.UnpricedCallsByService[b.Scope] > 0
	}
	return float64(u.TokensByService[b.Scope]), false
}

// EvaluateBudgets is the pure budget state machine: given the AI config, the
// month-to-date spend, the previous alerting state and the current time, it
// returns the next state (ai-owned keys only) and the notifications to deliver
// this tick.
//
// Green's function with the unit changed — same signature shape, same
// ok/warn/exceeded vocabulary, same straight ok→firing transition with no
// pending step. Each budget yields two logical rules, ai:<name>:warn and
// ai:<name>:over, both targeting the scope. Pure: no I/O, now injected.
func EvaluateBudgets(cfg Config, usage BudgetUsage, prev alerting.State, now time.Time) (alerting.State, []alerting.Notification) {
	next := alerting.State{}
	var notes []alerting.Notification
	for _, b := range cfg.Budgets {
		limit := b.Limit()
		if limit <= 0 {
			// Validate rejects this; the guard keeps the ratio finite for
			// direct-constructed configs (tests, defensive).
			continue
		}
		warn := b.WarnRatio
		if warn <= 0 {
			warn = DefaultWarnRatio
		}
		used, partial := usage.Used(b)
		ratio := used / limit
		target := budgetTarget(b)
		reason := budgetReason(b, used, ratio, partial)
		evalBudgetRule(next, &notes, prev, warnRuleName(b.Name), target, b.Channel, ratio >= warn, StatusWarn, reason, now)
		evalBudgetRule(next, &notes, prev, overRuleName(b.Name), target, b.Channel, ratio >= 1.0, StatusOver, reason, now)
	}
	return next, notes
}

// budgetTarget names what a budget is about. An estate-wide budget has no
// service to name, and "service:" with nothing after it would read as a service
// whose name is empty.
func budgetTarget(b Budget) string {
	if b.Scope == "" {
		return "estate"
	}
	return "service:" + b.Scope
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

// budgetReason is the human line recorded in the alert history, status-
// independent so both the fire and the resolve of a budget read the same
// snapshot. A partial number says so in the line itself: an operator reading
// "at 82%" needs to know when the real figure is higher.
func budgetReason(b Budget, used, ratio float64, partial bool) string {
	scope := "the estate"
	if b.Scope != "" {
		scope = fmt.Sprintf("service %q", b.Scope)
	}
	unit := "tokens"
	if b.IsCost() {
		unit = "cost"
	}
	line := fmt.Sprintf("budget %q for %s at %.0f%% (%.4g of %.4g %s)",
		b.Name, scope, ratio*100, used, b.Limit(), unit)
	if partial {
		line += " — at least, some calls in scope have no declared price"
	}
	return line
}
