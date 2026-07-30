package api

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The green module's HTTP surface: per-service energy (Wh) and carbon (gCO2e)
// over a window. Routes register only when the module is active (the
// 404-when-off contract). All carbon math happens here in Go — storage
// returns Wh only, factors never enter SQL (the AEP). Budgets live in
// green_budgets.go, the CSRD export in green_report.go.
// See design/2026-07-22-green-carbon.md.

// Names of the two synthetic summary rows. The unattributed bucket is energy
// whose pod could not be mapped to a workload (empty Service from storage);
// it is never folded into (other) — the coverage gap must stay visible.
const (
	greenOtherRow        = "(other)"
	greenUnattributedRow = "(unattributed)"
)

// greenConfig resolves the active green config, defaulting to Default() when
// none is wired (mirrors groupsConfig/alertsConfig).
func (a *API) greenConfig() green.Config {
	if a.cfg.GreenConfig != nil {
		return a.cfg.GreenConfig()
	}
	return green.Default()
}

// greenFactors is the resolved carbon conversion: grid intensity (gCO2e/kWh)
// with its provenance label, and the PUE multiplier.
type greenFactors struct {
	intensity float64
	source    string
	pue       float64
}

// resolveFactors reads the conversion factors off the config. PUE is
// re-defaulted for direct-constructed configs that skipped ParseConfig's
// normalize, mirroring EffectiveIntensity's own re-normalization.
func resolveFactors(cfg green.Config) greenFactors {
	intensity, source := cfg.EffectiveIntensity()
	pue := cfg.PUE
	if pue < 1 {
		pue = green.Default().PUE
	}
	return greenFactors{intensity: intensity, source: source, pue: pue}
}

// gco2e converts energy to carbon: gCO2e = Wh × intensity/1000 × PUE — the
// AEP formula, quoted literally in the report's methodology block
// (greenFormulaLiteral must stay in sync with this expression).
func (f greenFactors) gco2e(wh float64) float64 {
	return wh * f.intensity / 1000 * f.pue
}

// mgCO2ePerRequest is per-request carbon intensity in milligrams, with the
// div-zero guard: no requests → 0, and the DTO omits it via omitempty.
func mgCO2ePerRequest(gco2e float64, requests uint64) float64 {
	if requests == 0 {
		return 0
	}
	return gco2e * 1000 / float64(requests)
}

// greenQuery builds the storage query from the config's metric names — the
// backend must not hardcode Kepler naming. Empty names re-default like
// resolveFactors: direct-constructed configs skip ParseConfig's normalize.
func greenQuery(cfg green.Config, tenant string, tr storage.TimeRange, interval time.Duration) storage.GreenQuery {
	m, d := cfg.Metrics, green.Default().Metrics
	if len(m.PodEnergy) == 0 {
		m.PodEnergy = d.PodEnergy
	}
	if len(m.NodeEnergy) == 0 {
		m.NodeEnergy = d.NodeEnergy
	}
	if m.PodNameAttr == "" {
		m.PodNameAttr = d.PodNameAttr
	}
	if m.PodNamespaceAttr == "" {
		m.PodNamespaceAttr = d.PodNamespaceAttr
	}
	return storage.GreenQuery{
		Tenant:            tenant,
		Range:             tr,
		PodEnergyMetrics:  m.PodEnergy,
		NodeEnergyMetrics: m.NodeEnergy,
		PodNameAttr:       m.PodNameAttr,
		PodNamespaceAttr:  m.PodNamespaceAttr,
		Interval:          interval,
	}
}

// Wire DTOs for the green summary.

type greenFactorsDTO struct {
	GridIntensity   float64 `json:"gridIntensity"` // gCO2e/kWh
	IntensitySource string  `json:"intensitySource"`
	PUE             float64 `json:"pue"`
	// Dataset names the bundled factor table (Ember 2023/24). IntensitySource
	// is the authoritative provenance of the number in use ("operator-set" when
	// overridden); Dataset names only where the bundled defaults come from.
	Dataset string `json:"dataset"`
}

type greenTotalsDTO struct {
	AttributedWh float64 `json:"attributedWh"`
	// MeasuredWh / EstimatedWh split AttributedWh by quality tier (RAPL/
	// Kepler vs the tdp-estimator) — never silently blended into one number
	// (green TDP estimation AEP). Rows carrying no avuruops_quality
	// attribute at all (pre-AEP data) count toward neither.
	MeasuredWh     float64 `json:"measuredWh"`
	EstimatedWh    float64 `json:"estimatedWh"`
	UnattributedWh float64 `json:"unattributedWh"`
	// Coverage = attributed / (attributed + unattributed); 0 when nothing
	// was measured. Quoted by the report's methodology block.
	Coverage     float64         `json:"coverage"`
	GCO2e        float64         `json:"gco2e"`
	NodeCoverage nodeCoverageDTO `json:"nodeCoverage"`
}

type nodeCoverageDTO struct {
	Known     int `json:"known"`
	Measured  int `json:"measured"`
	Estimated int `json:"estimated"`
	Absent    int `json:"absent"`
}

type greenPointDTO struct {
	Time string  `json:"time"`
	Wh   float64 `json:"wh"`
}

type greenServiceDTO struct {
	Service string  `json:"service"`
	Wh      float64 `json:"wh"`
	// EstimatedWh is the portion of Wh that came from the tdp-estimator, not
	// Kepler/RAPL — 0 (omitted) when the service's energy is entirely
	// measured, never inferred, always summed from actual estimated rows.
	EstimatedWh float64 `json:"estimatedWh,omitempty"`
	GCO2e       float64 `json:"gco2e"`
	// Requests is the RED span count over the window; mgCO2ePerRequest is
	// omitted (not derived) when a service saw no requests.
	Requests         uint64          `json:"requests,omitempty"`
	MgCO2ePerRequest float64         `json:"mgCO2ePerRequest,omitempty"`
	Points           []greenPointDTO `json:"points,omitempty"`
}

type greenSummaryResponse struct {
	Window   healthWindowDTO   `json:"window"`
	Factors  greenFactorsDTO   `json:"factors"`
	Totals   greenTotalsDTO    `json:"totals"`
	Services []greenServiceDTO `json:"services"`
}

// handleGreenSummary reports per-service energy and carbon over the requested
// window: factors block, attributed/unattributed totals with the coverage
// ratio, and per-service rows with the RED request join and trend series.
func (a *API) handleGreenSummary(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	topN, err := parseInt(r, "topN", 0)
	if err != nil {
		return err
	}
	if topN < 0 {
		return badRequest("invalid topN: must be >= 0")
	}
	cfg := a.greenConfig()
	ten, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	rows, err := store.ServiceEnergy(r.Context(), greenQuery(cfg, ten, tr, 0))
	if err != nil {
		return err
	}
	requests, err := a.serviceRequests(r, store, ten, tr)
	if err != nil {
		return err
	}
	cov, err := store.NodeCoverage(r.Context(), greenQuery(cfg, ten, tr, 0))
	if err != nil {
		return err
	}
	f := resolveFactors(cfg)
	services, totals := buildGreenRows(rows, requests, f, topN)
	totals.NodeCoverage = nodeCoverageDTO{Known: cov.KnownNodes, Measured: cov.MeasuredNodes, Estimated: cov.EstimatedNodes, Absent: cov.AbsentNodes}
	writeJSON(w, http.StatusOK, greenSummaryResponse{
		Window:   healthWindowDTO{Start: tr.Start.UTC().Format(time.RFC3339), End: tr.End.UTC().Format(time.RFC3339)},
		Factors:  greenFactorsDTO{GridIntensity: f.intensity, IntensitySource: f.source, PUE: f.pue, Dataset: green.IntensityDataset},
		Totals:   totals,
		Services: services,
	})
	return nil
}

// serviceRequests joins request volume per service from the same span-count
// source the service map composes (ListServices over the window).
func (a *API) serviceRequests(r *http.Request, store storage.Store, tenant string, tr storage.TimeRange) (map[string]uint64, error) {
	stats, err := store.ListServices(r.Context(), storage.ServiceQuery{
		Tenant:     tenant,
		Range:      tr,
		ExcludeAux: !parseBool(r, "includeAux", false),
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]uint64, len(stats))
	for _, s := range stats {
		out[s.Name] = s.SpanCount
	}
	return out, nil
}

// buildGreenRows converts storage energy rows (heaviest-first) into wire rows:
// carbon conversion, request join, and the topN fold. With topN > 0 the
// attributed tail folds into one summed (other) row (no series). The
// unattributed bucket keeps its own named row, always last, never folded.
// A service may arrive as MULTIPLE storage rows (one per quality tier) —
// they are merged into exactly one output row per service before topN runs,
// so a service's measured and estimated energy always appear as one row
// with Wh = measured+estimated and EstimatedWh keeping the split visible
// (never silently blended into a single unlabeled number).
func buildGreenRows(rows []storage.ServiceEnergy, requests map[string]uint64, f greenFactors, topN int) ([]greenServiceDTO, greenTotalsDTO) {
	var totals greenTotalsDTO
	type acc struct {
		wh, estimatedWh float64
		points          []storage.EnergyPoint
	}
	byService := make(map[string]*acc, len(rows))
	order := make([]string, 0, len(rows))
	var unattrPoints []greenPointDTO
	hasUnattributed := false

	for _, row := range rows {
		if row.Service == "" {
			hasUnattributed = true
			totals.UnattributedWh += row.WattHours
			unattrPoints = append(unattrPoints, toGreenPoints(row.Points)...)
			continue
		}
		totals.AttributedWh += row.WattHours
		switch row.Quality {
		case "measured":
			totals.MeasuredWh += row.WattHours
		case "estimated":
			totals.EstimatedWh += row.WattHours
		}
		a, ok := byService[row.Service]
		if !ok {
			a = &acc{}
			byService[row.Service] = a
			order = append(order, row.Service)
		}
		a.wh += row.WattHours
		if row.Quality == "estimated" {
			a.estimatedWh += row.WattHours
		}
		a.points = append(a.points, row.Points...)
	}

	services := make([]greenServiceDTO, 0, len(order))
	for _, name := range order {
		a := byService[name]
		dto := toGreenServiceDTO(name, storage.ServiceEnergy{Service: name, WattHours: a.wh, Points: a.points}, requests[name], f)
		dto.EstimatedWh = a.estimatedWh
		services = append(services, dto)
	}
	sort.SliceStable(services, func(i, j int) bool {
		if services[i].Wh != services[j].Wh {
			return services[i].Wh > services[j].Wh
		}
		return services[i].Service < services[j].Service
	})

	if topN > 0 && len(services) > topN {
		other := greenServiceDTO{Service: greenOtherRow}
		for _, s := range services[topN:] {
			other.Wh += s.Wh
			other.EstimatedWh += s.EstimatedWh
			other.GCO2e += s.GCO2e
			other.Requests += s.Requests
		}
		other.MgCO2ePerRequest = mgCO2ePerRequest(other.GCO2e, other.Requests)
		services = append(services[:topN], other)
	}
	if hasUnattributed {
		services = append(services, greenServiceDTO{
			Service: greenUnattributedRow,
			Wh:      totals.UnattributedWh,
			GCO2e:   f.gco2e(totals.UnattributedWh),
			Points:  unattrPoints,
		})
	}
	totals.GCO2e = f.gco2e(totals.AttributedWh + totals.UnattributedWh)
	if sum := totals.AttributedWh + totals.UnattributedWh; sum > 0 {
		totals.Coverage = totals.AttributedWh / sum
	}
	return services, totals
}

func toGreenServiceDTO(name string, row storage.ServiceEnergy, requests uint64, f greenFactors) greenServiceDTO {
	out := greenServiceDTO{
		Service:  name,
		Wh:       row.WattHours,
		GCO2e:    f.gco2e(row.WattHours),
		Requests: requests,
		Points:   toGreenPoints(row.Points),
	}
	out.MgCO2ePerRequest = mgCO2ePerRequest(out.GCO2e, requests)
	return out
}

func toGreenPoints(points []storage.EnergyPoint) []greenPointDTO {
	if len(points) == 0 {
		return nil
	}
	out := make([]greenPointDTO, 0, len(points))
	for _, p := range points {
		out = append(out, greenPointDTO{Time: p.Time.UTC().Format(time.RFC3339), Wh: p.WattHours})
	}
	return out
}

// stampServiceEnergy overlays Wh/gCO2e onto the service-map rows when the
// green module is active. Best-effort by contract: on a storage error the map
// must still render, so the overlay is logged and skipped, never failed.
func (a *API) stampServiceEnergy(ctx context.Context, store storage.Store, tenant string, tr storage.TimeRange, services []serviceDTO) {
	cfg := a.greenConfig()
	rows, err := store.ServiceEnergy(ctx, greenQuery(cfg, tenant, tr, 0))
	if err != nil {
		slog.Warn("service-map energy overlay skipped", "error", err)
		return
	}
	f := resolveFactors(cfg)
	wh := make(map[string]float64, len(rows))
	for _, row := range rows {
		if row.Service != "" {
			wh[row.Service] = row.WattHours
		}
	}
	for i := range services {
		if v, ok := wh[services[i].Name]; ok {
			services[i].Wh = v
			services[i].GCO2e = f.gco2e(v)
		}
	}
}
