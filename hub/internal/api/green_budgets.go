package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Carbon budgets: calendar-month UTC month-to-date status per configured
// budget. Group membership resolves through health.Assign so carbon rolls up
// to the same groups the operator already sees in service health. Read-only
// status — warn/exceeded notifications ride the alerting tick, not this
// endpoint. See design/2026-07-22-green-carbon.md (budget lifecycle).

// Notification deliverability for one budget, resolved at read time. A budget
// whose crossing nobody will hear about is a silent budget, and the three
// failure modes want different fixes: turn the module on, give the budget a
// channel, or fix the name it points at. The UI says which — reporting only
// "alerting is off" hid the other two entirely.
const (
	notifyOK             = "ok"
	notifyAlertingOff    = "alerting-off"
	notifyNoChannel      = "no-channel"
	notifyUnknownChannel = "unknown-channel"
)

// budgetDelivery is the resolved notification plumbing: whether alerting runs
// at all, plus the channel names that resolve — UI-stored ∪ file-config, the
// same union the tick's channelResolver walks, so this endpoint cannot claim
// a delivery the tick would drop (or vice versa).
type budgetDelivery struct {
	alertingOn bool
	channels   map[string]bool
}

// status classifies one budget's deliverability. Budgets legitimately run
// without a channel (the AEP's dashboard-only degradation), so no-channel is
// a state to report, not an error.
func (d budgetDelivery) status(b green.Budget) string {
	switch {
	case !d.alertingOn:
		return notifyAlertingOff
	case strings.TrimSpace(b.Channel) == "":
		return notifyNoChannel
	case !d.channels[b.Channel]:
		return notifyUnknownChannel
	default:
		return notifyOK
	}
}

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
	// EstimatedShare is the fraction of UsedKgCO2e that came from modeled
	// (tdp-estimator) rather than measured (RAPL/Kepler) energy — budgets
	// include estimated energy (an all-VM fleet must be able to trip a
	// budget), but a threshold crossed mostly on modeled numbers must stay
	// visibly soft here and in any alert payload (green TDP estimation AEP).
	EstimatedShare float64 `json:"estimatedShare,omitempty"`
	// Notifications reports whether a threshold crossing on THIS budget can
	// actually be delivered: ok | alerting-off | no-channel | unknown-channel.
	Notifications string `json:"notifications"`
}

type greenBudgetsResponse struct {
	Window  healthWindowDTO  `json:"window"`
	Budgets []greenBudgetDTO `json:"budgets"`
	// Warnings are configuration problems that leave a budget inert without
	// making it look broken — today, a budget pointed at a group nothing rolls
	// up to. Surfaced here AND warn-logged on the tick.
	Warnings []string `json:"warnings,omitempty"`
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
	ten, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
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
	delivery, err := a.budgetDelivery(r.Context())
	if err != nil {
		return err
	}
	budgets, warnings := buildGreenBudgets(cfg, a.groupsConfig(r.Context()), resolveFactors(cfg), delivery, rows, stats, labels, now)
	writeJSON(w, http.StatusOK, greenBudgetsResponse{
		Window:   healthWindowDTO{Start: tr.Start.Format(time.RFC3339), End: tr.End.Format(time.RFC3339)},
		Budgets:  budgets,
		Warnings: warnings,
	})
	return nil
}

// budgetDelivery resolves the notification plumbing once per request. With
// alerting off the channel set is irrelevant (every budget reads
// alerting-off), so the store read is skipped entirely.
func (a *API) budgetDelivery(ctx context.Context) (budgetDelivery, error) {
	d := budgetDelivery{alertingOn: a.modules.Enabled(modules.Alerting), channels: map[string]bool{}}
	if !d.alertingOn {
		return d, nil
	}
	store, err := a.store()
	if err != nil {
		return d, err
	}
	stored, err := store.ListAlertChannels(ctx)
	if err != nil {
		return d, err
	}
	for _, ch := range stored {
		d.channels[ch.Name] = true
	}
	for _, ch := range a.alertsConfig().Channels {
		d.channels[ch.Name] = true
	}
	return d, nil
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
// service (used=0): the operator must see them. Returns the budgets plus any
// configuration warnings (unknown groups).
func buildGreenBudgets(cfg green.Config, groups health.Config, f greenFactors, delivery budgetDelivery, rows []storage.ServiceEnergy, stats []storage.ServiceStats, labels []storage.ServiceLabel, now time.Time) ([]greenBudgetDTO, []string) {
	assigned := assignEnergy(groups, rows, stats, labels)
	usedByGroup, estimatedByGroup := usedKgByGroup(f, assigned, rows)

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
			Notifications:   delivery.status(b),
		}
		if used > 0 {
			dto.EstimatedShare = estimatedByGroup[b.Group] / used
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
	return out, unknownGroupWarnings(cfg, knownGroups(groups, assigned))
}

// knownGroups is every group name a budget could sensibly target: the ones
// declared in the (config + DB) service-health configuration, plus the ones
// services actually landed in — auto-groups by namespace are legitimate
// targets, and a zero-config install has nothing but those.
func knownGroups(groups health.Config, assigned map[string]health.Assignment) map[string]bool {
	known := make(map[string]bool, len(groups.Groups)+len(assigned))
	for _, g := range groups.Groups {
		known[g.Name] = true
	}
	for _, a := range assigned {
		if a.Group != "" {
			known[a.Group] = true
		}
	}
	return known
}

// unknownGroupWarnings names budgets whose group nothing rolls up to. Such a
// budget is not an error — it renders at 0 and can never fire, which looks
// exactly like a group that simply had a quiet month. The warning is what
// tells those two apart. Sorted so the response is stable.
func unknownGroupWarnings(cfg green.Config, known map[string]bool) []string {
	var out []string
	for _, b := range cfg.Budgets {
		if b.Group == "" || known[b.Group] {
			continue
		}
		out = append(out, fmt.Sprintf("budget %q targets group %q, which matches no configured service group and no service seen this month — it will always read 0 and can never fire", b.Name, b.Group))
	}
	sort.Strings(out)
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

// usedKgByGroup rolls energy up to per-group kgCO2e, split into total and
// estimated-only — the alerting tick's usage source (BudgetUsageByGroup) and
// the budgets endpoint share this so both fire/report on identical numbers.
// The unattributed bucket (empty Service) has no group and never counts.
func usedKgByGroup(f greenFactors, assigned map[string]health.Assignment, rows []storage.ServiceEnergy) (total, estimated map[string]float64) {
	whByGroup := map[string]float64{}
	estWhByGroup := map[string]float64{}
	for _, row := range rows {
		if row.Service == "" {
			continue
		}
		g := assigned[row.Service].Group
		if g == "" {
			continue
		}
		whByGroup[g] += row.WattHours
		if row.Quality == "estimated" {
			estWhByGroup[g] += row.WattHours
		}
	}
	total = make(map[string]float64, len(whByGroup))
	estimated = make(map[string]float64, len(whByGroup))
	for g, wh := range whByGroup {
		total[g] = f.gco2e(wh) / 1000 // budgets are kg
		estimated[g] = f.gco2e(estWhByGroup[g]) / 1000
	}
	return total, estimated
}

// BudgetUsage is one tenant's month-to-date carbon roll-up plus the set of
// group names that exist to roll up TO. The tick needs both: usage to
// evaluate against, and the known set to tell a MISCONFIGURED budget (a group
// nothing can ever land in) from a merely idle one — a distinction usage
// alone cannot make, since both read 0.
type BudgetUsage struct {
	UsedKgCO2e  map[string]float64
	KnownGroups map[string]bool
}

// BudgetUsageByGroup computes one tenant's month-to-date used kgCO2e per
// serviceGroups group, composing the store reads (energy + population + labels)
// with the same roll-up the budgets endpoint uses. It is the alerting tick's
// usage source for green budget evaluation, kept here so the carbon math has
// exactly one implementation. now is injected (no clock read); factors never
// enter SQL — storage returns Wh, the conversion happens in Go (the AEP).
func BudgetUsageByGroup(ctx context.Context, store storage.Store, cfg green.Config, groups health.Config, tenant string, now time.Time) (BudgetUsage, error) {
	tr := monthToDate(now)
	rows, err := store.ServiceEnergy(ctx, greenQuery(cfg, tenant, tr, 0))
	if err != nil {
		return BudgetUsage{}, err
	}
	sq := storage.ServiceQuery{Tenant: tenant, Range: tr, ExcludeAux: true}
	stats, err := store.ListServices(ctx, sq)
	if err != nil {
		return BudgetUsage{}, err
	}
	labels, err := store.ServiceLabels(ctx, sq)
	if err != nil {
		return BudgetUsage{}, err
	}
	assigned := assignEnergy(groups, rows, stats, labels)
	usedByGroup, _ := usedKgByGroup(resolveFactors(cfg), assigned, rows)
	return BudgetUsage{UsedKgCO2e: usedByGroup, KnownGroups: knownGroups(groups, assigned)}, nil
}

// UnknownGroupWarnings is the exported form of the endpoint's own check, so
// the alerting tick warns on exactly the budgets the UI flags.
func UnknownGroupWarnings(cfg green.Config, known map[string]bool) []string {
	return unknownGroupWarnings(cfg, known)
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
