package main

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/ai"
	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// aiBudgets carries AI spend-budget evaluation into the alerting tick. Non-nil
// only when BOTH the ai and alerting modules are active — budgets ride the
// alerting persistence/delivery seam, so with alerting off they do not run and
// the AI module degrades to screens-only, the same path green takes.
//
// This file mirrors green_budgets.go closely and on purpose. Two budget ticks
// writing one alert_state with different merge and clobber-guard rules is
// precisely how the semantics drift apart; keeping them the same shape means a
// fix to one is an obvious fix to the other.
type aiBudgets struct {
	cfg    func() ai.Config
	usage  *aiUsageCache
	warned unknownScopeWarner
}

// newAIBudgets returns the budget-evaluation hook, or nil when either module is
// off. The nil-means-off contract keeps evaluateOnce's merge conditional on both
// modules without threading the module set all the way down.
func newAIBudgets(active modules.Set, cfg func() ai.Config, usage *aiUsageCache) *aiBudgets {
	if !active.Enabled(modules.AI) || !active.Enabled(modules.Alerting) {
		return nil
	}
	return &aiBudgets{cfg: cfg, usage: usage}
}

// aiUsageFn computes month-to-date AI spend for one tenant. The default wraps
// api.AIBudgetUsage (the shared roll-up); tests inject a counting fake to assert
// the cache throttles recompute.
type aiUsageFn func(ctx context.Context, store storage.Store, cfg ai.Config, tenant string, now time.Time) (ai.BudgetUsage, error)

// aiUsageCache throttles the SQL-backed spend roll-up to at most once per
// interval per tenant. Budget state advances only when usage is recomputed, so
// BOTH fire and resolve latency are bounded by the interval — unlike health
// alerts, which re-evaluate every tick. Only this query is cached.
type aiUsageCache struct {
	mu      sync.Mutex
	compute aiUsageFn
	entries map[string]aiUsageEntry
}

type aiUsageEntry struct {
	at   time.Time
	used ai.BudgetUsage
}

func newAIUsageCache(compute aiUsageFn) *aiUsageCache {
	return &aiUsageCache{compute: compute, entries: map[string]aiUsageEntry{}}
}

// get returns the tenant's cached usage when fresher than interval, else
// recomputes and caches it against now — the tick's injected clock, so the
// throttle and the month-rollover reset share one time source.
func (c *aiUsageCache) get(ctx context.Context, store storage.Store, cfg ai.Config, tenant string, now time.Time, interval time.Duration) (ai.BudgetUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[tenant]; ok && interval > 0 && now.Sub(e.at) < interval {
		return e.used, nil
	}
	used, err := c.compute(ctx, store, cfg, tenant, now)
	if err != nil {
		return ai.BudgetUsage{}, err
	}
	c.entries[tenant] = aiUsageEntry{at: now, used: used}
	return used, nil
}

// defaultAIBudgetUsage is the production usage source: the shared roll-up in the
// api package, so anything serving these numbers and the alerting tick fire on
// one implementation of the math.
func defaultAIBudgetUsage(ctx context.Context, store storage.Store, cfg ai.Config, tenant string, now time.Time) (ai.BudgetUsage, error) {
	return api.AIBudgetUsage(ctx, store, cfg, tenant, now)
}

// evalAIBudgets merges AI budget state into the alerting tick's next-state
// BEFORE diffToSave/deliver, for the same reason green does: diffToSave writes
// an explicit ok row for every prev key absent from next, and ai:* keys live in
// prev (the shared alert_state) but are NOT produced by alerting.Evaluate — so
// without this merge a firing budget would be clobbered to ok every tick. It
// mutates next in place and returns notes with the budget notifications
// appended. A no-op when ab is nil or no budgets are configured.
func evalAIBudgets(ctx context.Context, store storage.Store, ab *aiBudgets, tenant string, now time.Time, prev, next alerting.State, notes []alerting.Notification) []alerting.Notification {
	if ab == nil {
		return notes
	}
	cfg := ab.cfg()
	if len(cfg.Budgets) == 0 {
		return notes
	}
	used, err := ab.usage.get(ctx, store, cfg, tenant, now, cfg.BudgetCheckInterval())
	if err != nil {
		// A transient usage-compute failure must NOT clobber firing budgets:
		// carry ai-owned rows forward from prev so diffToSave keeps them.
		slog.Warn("alerting: ai budget usage failed, preserving prior budget state", "tenant", tenant, "error", err)
		for k, v := range prev {
			if strings.HasPrefix(k.Rule, ai.BudgetRulePrefix) {
				mergeAIKey(next, k, v)
			}
		}
		return notes
	}
	ab.warnUnknownScopes(cfg, used.KnownServices, tenant, now)
	aNext, aNotes := ai.EvaluateBudgets(cfg, used, prev, now)
	for k, v := range aNext {
		mergeAIKey(next, k, v)
	}
	return append(notes, aNotes...)
}

// mergeAIKey adds an ai-owned state key without ever overwriting one the health
// path already set. ai:* is a reserved namespace, but an operator could still
// define a health rule inside it; if so the health next wins, so budgets can
// never clobber a key alerting.Evaluate just populated.
func mergeAIKey(next alerting.State, k alerting.StateKey, v alerting.TargetState) {
	if _, ok := next[k]; !ok {
		next[k] = v
	}
}

// unknownScopeWarnLog is how often one budget's unknown-scope misconfiguration
// is repeated in the log. Hourly keeps the problem visible without burying
// everything else.
const unknownScopeWarnLog = time.Hour

// unknownScopeWarner throttles the unknown-scope warning to once per interval
// per (tenant, budget), against the tick's injected clock — the same source the
// usage cache and month rollover use, so tests never need a sleep. The zero
// value is ready to use.
type unknownScopeWarner struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (w *unknownScopeWarner) shouldWarn(key string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		w.last = map[string]time.Time{}
	}
	if at, ok := w.last[key]; ok && now.Sub(at) < unknownScopeWarnLog {
		return false
	}
	w.last[key] = now
	return true
}

// warnUnknownScopes reports a budget scoped to a service that has made no model
// call this month. Silence would be worse than noise here: such a budget reads
// as permanently healthy at 0% while measuring nothing at all, which is
// indistinguishable on a screen from a service comfortably under its ceiling.
func (ab *aiBudgets) warnUnknownScopes(cfg ai.Config, known map[string]bool, tenant string, now time.Time) {
	for _, b := range cfg.Budgets {
		if b.Scope == "" || known[b.Scope] {
			continue
		}
		if ab.warned.shouldWarn(tenant+"/"+b.Name, now) {
			slog.Warn("ai budget scoped to a service with no model calls this month",
				"tenant", tenant, "budget", b.Name, "scope", b.Scope)
		}
	}
}
