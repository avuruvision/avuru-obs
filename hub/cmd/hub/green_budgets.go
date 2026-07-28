package main

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// greenBudgets carries green-budget evaluation into the alerting tick. It is
// non-nil only when BOTH the green and alerting modules are active — budgets
// ride the alerting persistence/delivery seam, so with alerting off they simply
// don't run and green degrades to UI-only status (the AEP's documented
// green-without-alerting path). A nil value disables budget evaluation
// entirely, so the tick runs health alerting alone.
type greenBudgets struct {
	cfg   func() green.Config
	usage *budgetUsageCache
}

// newGreenBudgets returns the budget-evaluation hook, or nil when either module
// is off. The nil-means-off contract keeps evaluateOnce's merge conditional on
// both modules without threading the module set all the way down.
func newGreenBudgets(active modules.Set, cfg func() green.Config, usage *budgetUsageCache) *greenBudgets {
	if !active.Enabled(modules.Green) || !active.Enabled(modules.Alerting) {
		return nil
	}
	return &greenBudgets{cfg: cfg, usage: usage}
}

// budgetUsageFn computes month-to-date used kgCO2e per group for one tenant. The
// default wraps api.BudgetUsageByGroup (the shared carbon roll-up); tests inject
// a counting fake to assert the cache throttles recompute.
type budgetUsageFn func(ctx context.Context, store storage.Store, gcfg health.Config, cfg green.Config, tenant string, now time.Time) (map[string]float64, error)

// budgetUsageCache throttles the SQL-backed usage roll-up to at most once per
// interval per tenant. Because budget state advances only when usage is
// recomputed, BOTH fire and resolve latency are bounded by the interval (~5 min
// default) — unlike health alerts, which re-evaluate every tick. Only this
// query is cached; health alerting on the same tick is unaffected.
type budgetUsageCache struct {
	mu      sync.Mutex
	compute budgetUsageFn
	entries map[string]usageEntry
}

type usageEntry struct {
	at   time.Time
	used map[string]float64
}

func newBudgetUsageCache(compute budgetUsageFn) *budgetUsageCache {
	return &budgetUsageCache{compute: compute, entries: map[string]usageEntry{}}
}

// get returns the tenant's cached usage when fresher than interval, else
// recomputes and caches it against now — the tick's injected clock, so the
// throttle and month-rollover reset share one time source.
func (c *budgetUsageCache) get(ctx context.Context, store storage.Store, gcfg health.Config, cfg green.Config, tenant string, now time.Time, interval time.Duration) (map[string]float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[tenant]; ok && interval > 0 && now.Sub(e.at) < interval {
		return e.used, nil
	}
	used, err := c.compute(ctx, store, gcfg, cfg, tenant, now)
	if err != nil {
		return nil, err
	}
	c.entries[tenant] = usageEntry{at: now, used: used}
	return used, nil
}

// defaultBudgetUsage is the production usage source: the shared carbon roll-up
// in the api package, so the budgets endpoint and the alerting tick fire on one
// implementation of the math.
func defaultBudgetUsage(ctx context.Context, store storage.Store, gcfg health.Config, cfg green.Config, tenant string, now time.Time) (map[string]float64, error) {
	return api.BudgetUsageByGroup(ctx, store, cfg, gcfg, tenant, now)
}

// evalGreenBudgets merges green budget state into the alerting tick's next-state
// BEFORE diffToSave/deliver — the crux of Phase 5. diffToSave writes an explicit
// ok row for every prev key absent from next; green budget keys live in prev
// (the shared alert_state) but are NOT produced by alerting.Evaluate, so without
// this merge a firing budget would be clobbered to ok every tick. It mutates
// next in place and returns notes with the budget notifications appended. A
// no-op when gb is nil (green or alerting off) or no budgets are configured.
func evalGreenBudgets(ctx context.Context, store storage.Store, gcfg health.Config, gb *greenBudgets, tenant string, now time.Time, prev, next alerting.State, notes []alerting.Notification) []alerting.Notification {
	if gb == nil {
		return notes
	}
	cfg := gb.cfg()
	if len(cfg.Budgets) == 0 {
		return notes
	}
	used, err := gb.usage.get(ctx, store, gcfg, cfg, tenant, now, cfg.BudgetCheckInterval())
	if err != nil {
		// A transient usage-compute failure must NOT clobber firing budgets:
		// carry green-owned rows forward from prev so diffToSave keeps them.
		slog.Warn("alerting: green budget usage failed, preserving prior budget state", "tenant", tenant, "error", err)
		for k, v := range prev {
			if strings.HasPrefix(k.Rule, green.BudgetRulePrefix) {
				mergeGreenKey(next, k, v)
			}
		}
		return notes
	}
	gNext, gNotes := green.EvaluateBudgets(cfg, used, prev, now)
	for k, v := range gNext {
		mergeGreenKey(next, k, v)
	}
	return append(notes, gNotes...)
}

// mergeGreenKey adds a green-owned state key without ever overwriting one the
// health path already set. green:* is a reserved namespace, but an operator
// could still define a health rule inside it; if so the health next wins, so
// green can never clobber a key alerting.Evaluate just populated.
func mergeGreenKey(next alerting.State, k alerting.StateKey, v alerting.TargetState) {
	if _, ok := next[k]; !ok {
		next[k] = v
	}
}
