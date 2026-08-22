package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// captureNotifier records deliveries for evaluateOnce tests.
type captureNotifier struct {
	sent []alerting.Notification
	chs  []alerting.Channel
}

func (c *captureNotifier) Send(_ context.Context, ch alerting.Channel, n alerting.Notification) error {
	c.chs = append(c.chs, ch)
	c.sent = append(c.sent, n)
	return nil
}

// TestEvaluateOnceStampsTenantAndResolvesUIChannels drives one evaluator tick:
// a down service fires a for:0 rule; the delivered notification must carry the
// tenant, and the rule's channel must resolve from the UI store, shadowing the
// same-name config channel.
func TestEvaluateOnceStampsTenantAndResolvesUIChannels(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "payments", SpanCount: 100, ErrorCount: 50, P95: 100 * time.Millisecond}, // 50% errors -> down
		},
		Labels:  []storage.ServiceLabel{{Service: "payments", K8sNamespace: "prod-ns"}},
		Tenants: []string{"staging"},
		Channels: []storage.AlertChannel{
			{Name: "ops", Type: "webhook", URL: "https://hooks.example.com/ui", Secret: "ui-secret"},
		},
	}
	gcfg := health.Default()
	acfg := alerting.Config{
		EvalIntervalSec: 30, WindowMinutes: 5,
		Channels: []alerting.Channel{{Name: "ops", Type: "webhook", URL: "https://hooks.example.com/config"}},
		Rules: []alerting.Rule{{
			Name: "payments-down", When: alerting.WhenDown,
			Selector: alerting.Selector{Services: []string{"payments"}}, Channel: "ops",
		}},
	}
	notifier := &captureNotifier{}

	// Two ticks: ok -> pending (tick 1), pending -> firing + notify (tick 2).
	// A fixed clock (For:0 rules fire regardless); no green hook here.
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if err := evaluateOnce(context.Background(), func() storage.Store { return fake }, gcfg, acfg, notifier, nil, nil, now); err != nil {
			t.Fatalf("evaluateOnce: %v", err)
		}
		// Feed all saved state back chronologically; toEvalState is last-wins
		// per key, mirroring the ReplacingMergeTree.
		fake.AlertStates = nil
		for _, batch := range fake.SavedAlertStates {
			fake.AlertStates = append(fake.AlertStates, batch...)
		}
	}

	if len(notifier.sent) == 0 {
		t.Fatal("expected a delivered notification")
	}
	// tenantsToEvaluate = default ∪ observed(staging): both evaluate the same
	// data (the fake ignores the tenant), so each fires with its own stamp.
	tenants := map[string]bool{}
	for _, n := range notifier.sent {
		if n.Tenant == "" {
			t.Errorf("notification missing tenant: %+v", n)
		}
		tenants[n.Tenant] = true
		if n.Rule != "payments-down" || n.Kind != alerting.KindFired {
			t.Errorf("unexpected notification: %+v", n)
		}
	}
	if !tenants["default"] || !tenants["staging"] {
		t.Errorf("want notifications for default and staging tenants, got %v", tenants)
	}
	// The UI-stored channel shadows the config channel of the same name.
	for _, ch := range notifier.chs {
		if ch.URL != "https://hooks.example.com/ui" || ch.Secret != "ui-secret" {
			t.Errorf("channel should resolve from UI store, got %+v", ch)
		}
	}
}

// savedByRule flattens the last SaveAlertStates batch into a rule→status map.
func savedByRule(batch []storage.AlertState) map[string]string {
	out := make(map[string]string, len(batch))
	for _, s := range batch {
		out[s.RuleName] = s.Status
	}
	return out
}

// budgetGreenConfig is a green config whose factors make 1000 Wh == 1 kgCO2e
// (intensity 500, PUE 2), with one budget of 1 kg on group "shop".
func budgetGreenConfig() green.Config {
	return green.Config{
		GridIntensity: 500, PUE: 2,
		BudgetCheckIntervalSec: 300,
		Budgets: []green.Budget{{
			Name: "web", Group: "shop", MonthlyKgCO2e: 1, WarnRatio: 0.8, Channel: "ops",
		}},
	}
}

// TestEvaluateOnceMergesGreenBudgetsWithoutClobber is the Phase-5 hazard test:
// prev holds two FIRING green budget rows AND health produces its own next. The
// merge must put green's next-state into the same `next` map before diffToSave,
// so diffToSave does NOT supersede the still-firing budget with an explicit ok.
func TestEvaluateOnceMergesGreenBudgetsWithoutClobber(t *testing.T) {
	old := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 10},                                               // shop, healthy
			{Name: "payments", SpanCount: 100, ErrorCount: 50, P95: 100 * time.Millisecond}, // down
		},
		Labels: []storage.ServiceLabel{
			{Service: "checkout", K8sNamespace: "shop"},
			{Service: "payments", K8sNamespace: "prod-ns"},
		},
		// shop uses 900 Wh → 0.9 kg, ratio 0.9: warn (0.8) stays firing, over
		// (1.0) must resolve.
		ServiceEnergies: []storage.ServiceEnergy{{Service: "checkout", WattHours: 900}},
		// prev: BOTH budget rules firing from an earlier tick — the rows the
		// naive diffToSave would clobber to ok.
		AlertStates: []storage.AlertState{
			{Tenant: "default", RuleName: "green:web:warn", Target: "group:shop", Status: "firing", Since: old},
			{Tenant: "default", RuleName: "green:web:over", Target: "group:shop", Status: "firing", Since: old},
		},
	}
	acfg := alerting.Config{
		EvalIntervalSec: 30, WindowMinutes: 5,
		Channels: []alerting.Channel{{Name: "ops", Type: "webhook", URL: "https://hooks.example.com/ops"}},
		Rules: []alerting.Rule{{
			Name: "payments-down", When: alerting.WhenDown,
			Selector: alerting.Selector{Services: []string{"payments"}}, Channel: "ops",
		}},
	}
	gb := newGreenBudgets(
		modules.Set{modules.Green: true, modules.Alerting: true},
		func() green.Config { return budgetGreenConfig() },
		newBudgetUsageCache(defaultBudgetUsage),
	)
	notifier := &captureNotifier{}

	if err := evaluateOnce(context.Background(), func() storage.Store { return fake }, health.Default(), acfg, notifier, nil, gb, now); err != nil {
		t.Fatalf("evaluateOnce: %v", err)
	}

	if fake.LastGreenQuery.Tenant == "" {
		t.Fatal("green usage was never queried — budget eval did not run")
	}
	if len(fake.SavedAlertStates) != 1 {
		t.Fatalf("want one saved batch (one tenant), got %d", len(fake.SavedAlertStates))
	}
	saved := savedByRule(fake.SavedAlertStates[0])
	// THE HAZARD: the still-firing warn budget must NOT be reset to ok.
	if saved["green:web:warn"] != "firing" {
		t.Errorf("green:web:warn saved as %q, want firing (diffToSave clobber not prevented)", saved["green:web:warn"])
	}
	// The genuinely-recovered over budget resolves to ok.
	if saved["green:web:over"] != "ok" {
		t.Errorf("green:web:over saved as %q, want ok (should resolve at ratio 0.9)", saved["green:web:over"])
	}
	// Health still evaluated alongside green (payments ok→pending this tick).
	if saved["payments-down"] != "pending" {
		t.Errorf("payments-down saved as %q, want pending (health next must coexist)", saved["payments-down"])
	}
	// Exactly one green notification: the over-resolve. The dedup'd warn emits
	// nothing while it stays firing.
	var greenNotes []alerting.Notification
	for _, n := range notifier.sent {
		if n.Rule == "green:web:warn" {
			t.Errorf("warn must not re-notify while firing: %+v", n)
		}
		if n.Rule == "green:web:over" {
			greenNotes = append(greenNotes, n)
		}
	}
	if len(greenNotes) != 1 || greenNotes[0].Kind != alerting.KindResolved || greenNotes[0].Tenant != "default" {
		t.Errorf("green notifications = %+v, want one tenant-stamped over-resolve", greenNotes)
	}
}

// TestNewGreenBudgetsGatedOnBothModules: budget eval is wired only when BOTH
// green and alerting are active — the AEP's green-without-alerting degradation.
func TestNewGreenBudgetsGatedOnBothModules(t *testing.T) {
	cfg := func() green.Config { return green.Default() }
	cache := newBudgetUsageCache(defaultBudgetUsage)
	if newGreenBudgets(modules.Set{modules.Green: true, modules.Alerting: false}, cfg, cache) != nil {
		t.Error("alerting off must disable green budget eval")
	}
	if newGreenBudgets(modules.Set{modules.Green: false, modules.Alerting: true}, cfg, cache) != nil {
		t.Error("green off must disable green budget eval")
	}
	if newGreenBudgets(modules.Set{modules.Green: true, modules.Alerting: true}, cfg, cache) == nil {
		t.Error("both on must enable green budget eval")
	}
}

// TestEvaluateOnceSkipsGreenWhenHookNil: with a nil hook (green module off) the
// tick issues no energy query and writes no green rows, even with a crossing
// budget config and energy present.
func TestEvaluateOnceSkipsGreenWhenHookNil(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	fake := &storagetest.Fake{
		Services:        []storage.ServiceStats{{Name: "checkout", SpanCount: 10}},
		Labels:          []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
		ServiceEnergies: []storage.ServiceEnergy{{Service: "checkout", WattHours: 5000}}, // would blow the budget
	}
	if err := evaluateOnce(context.Background(), func() storage.Store { return fake }, health.Default(), alerting.Default(), &captureNotifier{}, nil, nil, now); err != nil {
		t.Fatalf("evaluateOnce: %v", err)
	}
	if fake.LastGreenQuery.Tenant != "" {
		t.Error("green usage must not be queried when the hook is nil")
	}
	for _, batch := range fake.SavedAlertStates {
		for _, s := range batch {
			if len(s.RuleName) >= len(green.BudgetRulePrefix) && s.RuleName[:len(green.BudgetRulePrefix)] == green.BudgetRulePrefix {
				t.Errorf("no green rows expected when green off, got %+v", s)
			}
		}
	}
}

// TestBudgetUsageCacheThrottlesRecompute: the SQL-backed usage roll-up recomputes
// at most once per BudgetCheckInterval per tenant — two ticks inside the window
// compute once; a tick past the window recomputes.
func TestBudgetUsageCacheThrottlesRecompute(t *testing.T) {
	var calls int
	counting := func(_ context.Context, _ storage.Store, _ health.Config, _ green.Config, _ string, _ time.Time) (api.BudgetUsage, error) {
		calls++
		return api.BudgetUsage{
			UsedKgCO2e:  map[string]float64{"shop": 0.9},
			KnownGroups: map[string]bool{"shop": true},
		}, nil
	}
	gb := newGreenBudgets(
		modules.Set{modules.Green: true, modules.Alerting: true},
		func() green.Config { return budgetGreenConfig() }, // interval 300s
		newBudgetUsageCache(counting),
	)
	fake := &storagetest.Fake{}
	run := func(now time.Time) {
		if err := evaluateOnce(context.Background(), func() storage.Store { return fake }, health.Default(), alerting.Default(), &captureNotifier{}, nil, gb, now); err != nil {
			t.Fatalf("evaluateOnce: %v", err)
		}
	}
	t0 := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	run(t0)
	run(t0.Add(2 * time.Minute)) // within the 5-min window → cached
	if calls != 1 {
		t.Fatalf("usage computed %d times within interval, want 1", calls)
	}
	run(t0.Add(6 * time.Minute)) // past the window → recompute
	if calls != 2 {
		t.Fatalf("usage computed %d times after interval, want 2", calls)
	}
}

// TestEvaluateOnceChannelLessBudgetIsDashboardOnly: a budget with no delivery
// channel is valid config (the AEP's dashboard-only degradation, not an error).
// Its crossing must record state + history but attempt no send and log no
// "unknown channel" warning — otherwise every tick would warn-spam the operator.
func TestEvaluateOnceChannelLessBudgetIsDashboardOnly(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	fake := &storagetest.Fake{
		Services:        []storage.ServiceStats{{Name: "checkout", SpanCount: 10}},
		Labels:          []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
		ServiceEnergies: []storage.ServiceEnergy{{Service: "checkout", WattHours: 900}}, // 0.9 kg → warn crosses
	}
	// Budget with an empty Channel: dashboard-only.
	cfg := green.Config{
		GridIntensity: 500, PUE: 2, BudgetCheckIntervalSec: 300,
		Budgets: []green.Budget{{Name: "web", Group: "shop", MonthlyKgCO2e: 1, WarnRatio: 0.8}},
	}
	gb := newGreenBudgets(
		modules.Set{modules.Green: true, modules.Alerting: true},
		func() green.Config { return cfg },
		newBudgetUsageCache(defaultBudgetUsage),
	)
	notifier := &captureNotifier{}

	// Capture WARN logs to prove no "unknown channel" noise.
	var logbuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logbuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	if err := evaluateOnce(context.Background(), func() storage.Store { return fake }, health.Default(), alerting.Default(), notifier, nil, gb, now); err != nil {
		t.Fatalf("evaluateOnce: %v", err)
	}

	if len(notifier.sent) != 0 {
		t.Errorf("channel-less budget must attempt no delivery, sent %+v", notifier.sent)
	}
	if strings.Contains(logbuf.String(), "unknown channel") {
		t.Errorf("channel-less budget must not warn about an unknown channel; log:\n%s", logbuf.String())
	}
	// State recorded: the warn crossing persists as firing.
	if len(fake.SavedAlertStates) != 1 {
		t.Fatalf("want one saved batch, got %d", len(fake.SavedAlertStates))
	}
	if savedByRule(fake.SavedAlertStates[0])["green:web:warn"] != "firing" {
		t.Errorf("state not recorded: %+v", fake.SavedAlertStates[0])
	}
	// History recorded: the crossing lands in the alerts timeline regardless.
	var found bool
	for _, batch := range fake.AppendedHistory {
		for _, h := range batch {
			if h.RuleName == "green:web:warn" && h.Status == green.StatusWarn {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("crossing not recorded in history: %+v", fake.AppendedHistory)
	}
}

// TestGreenBudgetUnknownGroupWarnsOnceAnHour: a budget aimed at a group
// nothing rolls up to is warn-logged — it can never fire, and on a dashboard
// that is indistinguishable from a quiet month. But the tick runs every 30s,
// so the same misconfiguration must not be re-logged on every pass.
func TestGreenBudgetUnknownGroupWarnsOnceAnHour(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	fake := &storagetest.Fake{
		Services:        []storage.ServiceStats{{Name: "checkout", SpanCount: 10}},
		Labels:          []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
		ServiceEnergies: []storage.ServiceEnergy{{Service: "checkout", WattHours: 900}},
	}
	cfg := green.Config{
		GridIntensity: 500, PUE: 2, BudgetCheckIntervalSec: 1, // never cache across ticks
		Budgets: []green.Budget{
			{Name: "live", Group: "shop", MonthlyKgCO2e: 1, WarnRatio: 0.8},
			{Name: "ghost", Group: "team-that-left", MonthlyKgCO2e: 1, WarnRatio: 0.8},
		},
	}
	gb := newGreenBudgets(
		modules.Set{modules.Green: true, modules.Alerting: true},
		func() green.Config { return cfg },
		newBudgetUsageCache(defaultBudgetUsage),
	)

	var logbuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logbuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	run := func(at time.Time) {
		if err := evaluateOnce(context.Background(), func() storage.Store { return fake }, health.Default(), alerting.Default(), &captureNotifier{}, nil, gb, at); err != nil {
			t.Fatalf("evaluateOnce: %v", err)
		}
	}
	count := func() int { return strings.Count(logbuf.String(), "unknown group") }

	run(now)
	if count() != 1 {
		t.Fatalf("first tick logged %d unknown-group warnings, want 1:\n%s", count(), logbuf.String())
	}
	if !strings.Contains(logbuf.String(), "team-that-left") {
		t.Errorf("warning does not name the offending group:\n%s", logbuf.String())
	}
	if strings.Contains(logbuf.String(), `budget \"live\"`) {
		t.Errorf("the live budget must not be warned about:\n%s", logbuf.String())
	}

	run(now.Add(30 * time.Second))
	run(now.Add(59 * time.Minute))
	if count() != 1 {
		t.Fatalf("warning repeated inside the hour (%d):\n%s", count(), logbuf.String())
	}

	run(now.Add(61 * time.Minute))
	if count() != 2 {
		t.Fatalf("warning not repeated after the hour (%d):\n%s", count(), logbuf.String())
	}
}
