package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Carbon budgets: calendar-month UTC month-to-date status per configured
// budget. Group membership resolves through health.Assign so carbon rolls up
// to the same groups the operator already sees in service health. Read-only
// status — warn/exceeded notifications ride the alerting tick, not this
// endpoint. See design/2026-07-22-green-carbon.md (budget lifecycle).

type greenBurnPointDTO struct {
	Time string `json:"time"`
	// KgCO2e is cumulative, in the budget's own unit, so the UI plots the
	// burn-down straight against monthlyKgCO2e.
	KgCO2e float64 `json:"kgCO2e"`
}

type greenBudgetDTO struct {
	Name          string  `json:"name"`
	Group         string  `json:"group"`
	MonthlyKgCO2e float64 `json:"monthlyKgCO2e"`
	UsedKgCO2e    float64 `json:"usedKgCO2e"`
	// ProjectedKgCO2e is the linear month-end projection: used divided by the
	// elapsed fraction of the calendar month.
	ProjectedKgCO2e float64             `json:"projectedKgCO2e"`
	Ratio           float64             `json:"ratio"`
	Status          string              `json:"status"` // ok | warn | exceeded
	BurnDown        []greenBurnPointDTO `json:"burnDown,omitempty"`
}

type greenBudgetsResponse struct {
	Window  healthWindowDTO  `json:"window"`
	Budgets []greenBudgetDTO `json:"budgets"`
}

// handleGreenBudgets reports month-to-date usage per configured budget. The
// window is always the current UTC calendar month regardless of query params
// — budgets are monthly by definition (the AEP).
func (a *API) handleGreenBudgets(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	cfg := a.greenConfig()
	now := time.Now().UTC()
	tr := monthToDate(now)
	ten := tenant(r)
	// Daily buckets: one query serves both the used totals and the burn-down.
	rows, err := store.ServiceEnergy(r.Context(), greenQuery(cfg, ten, tr, 24*time.Hour))
	if err != nil {
		return err
	}
	// Group assignment reads the same population service-health groups from;
	// aux traffic stays excluded like the health rollup's default.
	sq := storage.ServiceQuery{Tenant: ten, Range: tr, ExcludeAux: true}
	stats, err := store.ListServices(r.Context(), sq)
	if err != nil {
		return err
	}
	labels, err := store.ServiceLabels(r.Context(), sq)
	if err != nil {
		return err
	}
	budgets := buildGreenBudgets(cfg, a.groupsConfig(), resolveFactors(cfg), rows, stats, labels, now)
	writeJSON(w, http.StatusOK, greenBudgetsResponse{
		Window:  healthWindowDTO{Start: tr.Start.Format(time.RFC3339), End: tr.End.Format(time.RFC3339)},
		Budgets: budgets,
	})
	return nil
}

// monthStartUTC is the first instant of now's UTC calendar month.
func monthStartUTC(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// monthToDate is the UTC calendar-month window ending at now.
func monthToDate(now time.Time) storage.TimeRange {
	return storage.TimeRange{Start: monthStartUTC(now), End: now}
}

// buildGreenBudgets computes per-budget usage, projection, status, and the
// cumulative burn-down. Pure (now injected) so the projection math is
// testable without month-boundary flakes — the alerting-evaluator convention.
// Every configured budget appears, including ones whose group matches no live
// service (used=0): the operator must see them.
func buildGreenBudgets(cfg green.Config, groups health.Config, f greenFactors, rows []storage.ServiceEnergy, stats []storage.ServiceStats, labels []storage.ServiceLabel, now time.Time) []greenBudgetDTO {
	assigned := assignEnergy(groups, rows, stats, labels)
	usedByGroup := usedKgByGroup(f, assigned, rows)

	monthStart := monthStartUTC(now)
	monthFrac := elapsedMonthFraction(monthStart, now)

	out := make([]greenBudgetDTO, 0, len(cfg.Budgets))
	for _, b := range cfg.Budgets {
		// Same carbon roll-up the alerting tick fires on (usedKgByGroup); the
		// per-budget bucket loop below adds only the UI burn-down series.
		used := usedByGroup[b.Group]
		buckets := map[time.Time]float64{}
		for _, row := range rows {
			// The unattributed bucket never counts toward a budget: it has no
			// service, hence no group.
			if row.Service == "" || assigned[row.Service].Group != b.Group {
				continue
			}
			for _, p := range row.Points {
				buckets[p.Time.UTC()] += p.WattHours
			}
		}
		dto := greenBudgetDTO{
			Name:            b.Name,
			Group:           b.Group,
			MonthlyKgCO2e:   b.MonthlyKgCO2e,
			UsedKgCO2e:      used,
			ProjectedKgCO2e: used,
			Status:          green.StatusOK,
			BurnDown:        burnDown(buckets, f),
		}
		if monthFrac > 0 {
			dto.ProjectedKgCO2e = used / monthFrac
		}
		if b.MonthlyKgCO2e > 0 {
			dto.Ratio = used / b.MonthlyKgCO2e
		}
		warn := b.WarnRatio
		if warn <= 0 {
			warn = green.DefaultWarnRatio // re-applied for direct-constructed configs
		}
		switch {
		case dto.Ratio >= 1:
			dto.Status = green.StatusOver
		case dto.Ratio >= warn:
			dto.Status = green.StatusWarn
		}
		out = append(out, dto)
	}
	return out
}

// assignEnergy maps every energy-bearing service to its service-health group.
// Energy can exist outside the RED population (batch workloads emitting no entry
// spans), so it synthesises a zero-stats row per energy-only service before
// health.Assign — otherwise that carbon would silently vanish from every group.
func assignEnergy(groups health.Config, rows []storage.ServiceEnergy, stats []storage.ServiceStats, labels []storage.ServiceLabel) map[string]health.Assignment {
	pop := append([]storage.ServiceStats(nil), stats...)
	seen := make(map[string]bool, len(pop))
	for _, s := range pop {
		seen[s.Name] = true
	}
	for _, row := range rows {
		if row.Service != "" && !seen[row.Service] {
			pop = append(pop, storage.ServiceStats{Name: row.Service})
			seen[row.Service] = true
		}
	}
	return health.Assign(groups, pop, labels)
}

// usedKgByGroup rolls energy up to per-group kgCO2e — the single carbon roll-up
// shared by the budgets endpoint (buildGreenBudgets) and the alerting tick
// (BudgetUsageByGroup), so both compute and fire on identical numbers. The
// unattributed bucket (empty Service) has no group and never counts.
func usedKgByGroup(f greenFactors, assigned map[string]health.Assignment, rows []storage.ServiceEnergy) map[string]float64 {
	whByGroup := map[string]float64{}
	for _, row := range rows {
		if row.Service == "" {
			continue
		}
		g := assigned[row.Service].Group
		if g == "" {
			continue
		}
		whByGroup[g] += row.WattHours
	}
	out := make(map[string]float64, len(whByGroup))
	for g, wh := range whByGroup {
		out[g] = f.gco2e(wh) / 1000 // budgets are kg
	}
	return out
}

// BudgetUsageByGroup computes one tenant's month-to-date used kgCO2e per
// serviceGroups group, composing the store reads (energy + population + labels)
// with the same roll-up the budgets endpoint uses. It is the alerting tick's
// usage source for green budget evaluation, kept here so the carbon math has
// exactly one implementation. now is injected (no clock read); factors never
// enter SQL — storage returns Wh, the conversion happens in Go (the AEP).
func BudgetUsageByGroup(ctx context.Context, store storage.Store, cfg green.Config, groups health.Config, tenant string, now time.Time) (map[string]float64, error) {
	tr := monthToDate(now)
	rows, err := store.ServiceEnergy(ctx, greenQuery(cfg, tenant, tr, 0))
	if err != nil {
		return nil, err
	}
	sq := storage.ServiceQuery{Tenant: tenant, Range: tr, ExcludeAux: true}
	stats, err := store.ListServices(ctx, sq)
	if err != nil {
		return nil, err
	}
	labels, err := store.ServiceLabels(ctx, sq)
	if err != nil {
		return nil, err
	}
	return usedKgByGroup(resolveFactors(cfg), assignEnergy(groups, rows, stats, labels), rows), nil
}

// elapsedMonthFraction is how far through the calendar month now is, in [0,1].
func elapsedMonthFraction(monthStart, now time.Time) float64 {
	total := monthStart.AddDate(0, 1, 0).Sub(monthStart)
	elapsed := now.Sub(monthStart)
	if elapsed <= 0 || total <= 0 {
		return 0
	}
	if elapsed > total {
		return 1
	}
	return float64(elapsed) / float64(total)
}

// burnDown folds daily Wh buckets into a cumulative kgCO2e series.
func burnDown(buckets map[time.Time]float64, f greenFactors) []greenBurnPointDTO {
	if len(buckets) == 0 {
		return nil
	}
	times := make([]time.Time, 0, len(buckets))
	for t := range buckets {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	out := make([]greenBurnPointDTO, 0, len(times))
	var cumWh float64
	for _, t := range times {
		cumWh += buckets[t]
		out = append(out, greenBurnPointDTO{Time: t.Format(time.RFC3339), KgCO2e: f.gco2e(cumWh) / 1000})
	}
	return out
}
