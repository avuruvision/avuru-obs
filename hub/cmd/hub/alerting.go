package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// loadAlertingConfig loads AVURUOBS_ALERTS_CONFIG and returns a hot-reloading
// accessor for it (see loadHotReload). An unset path yields Default() (inert).
func loadAlertingConfig(ctx context.Context) (func() alerting.Config, error) {
	return loadHotReload(ctx, "AVURUOBS_ALERTS_CONFIG", "alerting config",
		alerting.Default(), alerting.ParseConfig,
		func(cfg alerting.Config) []any {
			return []any{"rules", len(cfg.Rules), "channels", len(cfg.Channels)}
		})
}

// webhookAllowCIDRs parses AVURUOBS_WEBHOOK_ALLOW (comma-separated CIDRs) — the
// operator override that lets alerting reach a receiver on a private network.
func webhookAllowCIDRs() []*net.IPNet {
	var out []*net.IPNet
	for _, part := range splitCSV(os.Getenv("AVURUOBS_WEBHOOK_ALLOW")) {
		if _, cidr, err := net.ParseCIDR(strings.TrimSpace(part)); err == nil {
			out = append(out, cidr)
		} else {
			slog.Warn("ignoring invalid AVURUOBS_WEBHOOK_ALLOW entry", "value", part, "error", err)
		}
	}
	return out
}

// runAlertingEvaluator is the single background evaluator: every interval it
// computes health per tenant, runs the alerting state machine against the
// persisted state, delivers notifications, and writes state + history back.
// v1 assumes ONE active evaluator (hub replicas default to 1); >1 would
// duplicate notifications (documented, HA leader election is v2).
// groups is the same *health.Resolver the API is given (see main): the group
// set alerts fire on must be the group set the screen shows.
func runAlertingEvaluator(ctx context.Context, provider api.StoreProvider, gate *schemaGate, groups *health.Resolver, alertsCfg func() alerting.Config, greenCfg func() green.Config, notifier alerting.Notifier, projects []string, active modules.Set) {
	// Every tick reads otel_traces and alert_channel; without the schema each
	// one used to warn twice per interval, forever. Wait instead — the gate
	// reports the missing schema once, and heals it when it can.
	if !gate.wait(ctx) {
		return
	}
	slog.Info("alerting evaluator started (single active evaluator)")
	// Green budgets ride this same tick (never a second ticker) so they share
	// the diffToSave/deliver path and cannot race it. nil unless both modules
	// are active; the per-tenant usage cache lives here, across ticks.
	gb := newGreenBudgets(active, greenCfg, newBudgetUsageCache(defaultBudgetUsage))
	if gb != nil {
		slog.Info("green budgets evaluated in the alerting tick")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(alertsCfg().Interval()):
		}
		if err := evaluateOnce(ctx, provider, groups.Config(ctx), alertsCfg(), notifier, projects, gb, time.Now().UTC()); err != nil {
			slog.Warn("alerting evaluation tick failed", "error", err)
		}
	}
}

func evaluateOnce(ctx context.Context, provider api.StoreProvider, gcfg health.Config, acfg alerting.Config, notifier alerting.Notifier, projects []string, gb *greenBudgets, now time.Time) error {
	store := provider()
	if store == nil {
		return fmt.Errorf("store unavailable")
	}
	resolve := channelResolver(ctx, store, acfg)
	for _, tenant := range tenantsToEvaluate(ctx, store, projects) {
		report, err := computeHealth(ctx, store, gcfg, tenant, now, acfg.Window())
		if err != nil {
			slog.Warn("alerting: health compute failed", "tenant", tenant, "error", err)
			continue
		}
		prevRows, err := store.LoadAlertStates(ctx, tenant)
		if err != nil {
			slog.Warn("alerting: load state failed", "tenant", tenant, "error", err)
			continue
		}
		// One prev snapshot feeds health, green, and diffToSave — green budget
		// keys live in the same alert_state, and their next-state must land in
		// `next` before diffToSave so a firing budget is not superseded by ok.
		prev := toEvalState(prevRows)
		next, notes := alerting.Evaluate(acfg, report, prev, now)
		notes = evalGreenBudgets(ctx, store, gcfg, gb, tenant, now, prev, next, notes)
		for i := range notes {
			notes[i].Tenant = tenant
		}
		deliver(ctx, notifier, resolve, notes)
		if err := store.SaveAlertStates(ctx, diffToSave(tenant, prev, next, now)); err != nil {
			slog.Warn("alerting: save state failed", "tenant", tenant, "error", err)
		}
		if hist := toHistory(tenant, notes); len(hist) > 0 {
			if err := store.AppendAlertHistory(ctx, hist); err != nil {
				slog.Warn("alerting: append history failed", "tenant", tenant, "error", err)
			}
		}
	}
	return nil
}

// computeHealth fetches the RED population + labels + edges for one tenant and
// rolls it up — the same inputs the /health API uses, evaluated here.
func computeHealth(ctx context.Context, store storage.Store, gcfg health.Config, tenant string, now time.Time, window time.Duration) (health.Report, error) {
	q := storage.ServiceQuery{Tenant: tenant, Range: storage.TimeRange{Start: now.Add(-window), End: now}, ExcludeAux: true}
	stats, err := store.ListServices(ctx, q)
	if err != nil {
		return health.Report{}, err
	}
	labels, err := store.ServiceLabels(ctx, q)
	if err != nil {
		return health.Report{}, err
	}
	edges, err := store.ServiceEdges(ctx, q)
	if err != nil {
		return health.Report{}, err
	}
	return health.Rollup(gcfg, window, stats, labels, edges), nil
}

// tenantsToEvaluate is the config projects ∪ tenants observed in data.
func tenantsToEvaluate(ctx context.Context, store storage.Store, projects []string) []string {
	set := map[string]bool{storage.DefaultTenant: true}
	for _, p := range projects {
		set[p] = true
	}
	if ts, err := store.ListTenants(ctx); err == nil {
		for _, t := range ts {
			set[t] = true
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	return out
}

// channelResolver merges UI-stored channels (which win on name collision) over
// file-config channels, loaded once per tick — channels are global, not
// per-tenant. Store errors degrade to config-only resolution (logged).
func channelResolver(ctx context.Context, store storage.Store, acfg alerting.Config) func(string) (alerting.Channel, bool) {
	stored, err := store.ListAlertChannels(ctx)
	if err != nil {
		slog.Warn("alerting: list stored channels failed, using config channels only", "error", err)
		stored = nil
	}
	byName := make(map[string]alerting.Channel, len(stored))
	for _, ch := range stored {
		byName[ch.Name] = alerting.Channel{Name: ch.Name, Type: ch.Type, URL: ch.URL, Secret: ch.Secret}
	}
	return func(name string) (alerting.Channel, bool) {
		if ch, ok := byName[name]; ok {
			return ch, true
		}
		return acfg.ChannelByName(name)
	}
}

func deliver(ctx context.Context, notifier alerting.Notifier, resolve func(string) (alerting.Channel, bool), notes []alerting.Notification) {
	for _, n := range notes {
		if strings.TrimSpace(n.Channel) == "" {
			// A channel-less notification is a green budget with no delivery
			// channel configured — the AEP's dashboard-only degradation, not an
			// error, so skip delivery silently (state + history still record the
			// crossing). Alerting's own rules reject an empty channel at config
			// parse, so this can only be a green budget: nothing to warn about.
			continue
		}
		ch, ok := resolve(n.Channel)
		if !ok {
			slog.Warn("alerting: rule references unknown channel", "channel", n.Channel, "rule", n.Rule)
			continue
		}
		if err := notifier.Send(ctx, ch, n); err != nil {
			// The transition is real; delivery failed after retries. Log and
			// carry on — v1 does not queue failed deliveries (documented).
			slog.Warn("alerting: delivery failed", "rule", n.Rule, "target", n.Target, "kind", n.Kind, "error", err)
		}
	}
}

func toEvalState(rows []storage.AlertState) alerting.State {
	st := alerting.State{}
	for _, r := range rows {
		st[alerting.StateKey{Rule: r.RuleName, Target: r.Target}] = alerting.TargetState{
			Status: r.Status, Since: r.Since, LastNotifiedAt: r.LastNotifiedAt,
		}
	}
	return st
}

// diffToSave writes the next state, plus an explicit 'ok' row for every key that
// was active last tick but is gone now, so the ReplacingMergeTree supersedes the
// stale firing/pending row.
func diffToSave(tenant string, prev, next alerting.State, now time.Time) []storage.AlertState {
	var out []storage.AlertState
	for k, v := range next {
		out = append(out, storage.AlertState{
			Tenant: tenant, RuleName: k.Rule, Target: k.Target,
			Status: v.Status, Since: v.Since, LastNotifiedAt: v.LastNotifiedAt,
		})
	}
	for k, v := range prev {
		if _, still := next[k]; !still {
			out = append(out, storage.AlertState{
				Tenant: tenant, RuleName: k.Rule, Target: k.Target,
				Status: "ok", Since: now, LastNotifiedAt: v.LastNotifiedAt,
			})
		}
	}
	return out
}

func toHistory(tenant string, notes []alerting.Notification) []storage.AlertHistoryEntry {
	out := make([]storage.AlertHistoryEntry, 0, len(notes))
	for _, n := range notes {
		out = append(out, storage.AlertHistoryEntry{
			Tenant: tenant, RuleName: n.Rule, Target: n.Target,
			Kind: n.Kind, Status: n.Status, Reason: n.Reason, FiredAt: n.At,
		})
	}
	return out
}
