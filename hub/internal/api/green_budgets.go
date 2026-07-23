package api

import (
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
	// Energy can exist outside the RED population (batch workloads emitting
	// no entry spans). Assign only covers the stats set, so synthesize a
	// zero-stats row per energy-only service — otherwise its carbon would
	// silently vanish from every budget.
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
	assigned := health.Assign(groups, pop, labels)

	monthStart := monthStartUTC(now)
	monthFrac := elapsedMonthFraction(monthStart, now)

	out := make([]greenBudgetDTO, 0, len(cfg.Budgets))
	for _, b := range cfg.Budgets {
		var usedWh float64
		buckets := map[time.Time]float64{}
		for _, row := range rows {
			// The unattributed bucket never counts toward a budget: it has no
			// service, hence no group.
			if row.Service == "" || assigned[row.Service].Group != b.Group {
				continue
			}
			usedWh += row.WattHours
			for _, p := range row.Points {
				buckets[p.Time.UTC()] += p.WattHours
			}
		}
		used := f.gco2e(usedWh) / 1000 // budgets are kg
		dto := greenBudgetDTO{
			Name:            b.Name,
			Group:           b.Group,
			MonthlyKgCO2e:   b.MonthlyKgCO2e,
			UsedKgCO2e:      used,
			ProjectedKgCO2e: used,
			Status:          "ok",
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
			dto.Status = "exceeded"
		case dto.Ratio >= warn:
			dto.Status = "warn"
		}
		out = append(out, dto)
	}
	return out
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
