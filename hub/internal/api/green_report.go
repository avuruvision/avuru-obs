package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The CSRD export: per-service energy/carbon numbers plus the methodology
// block an auditor needs to reproduce them (formula, factor provenance,
// coverage + gaps, metric names, hub version). One shared builder
// (buildGreenRows) feeds both wire formats; CSV is deliberately boring —
// encoding/csv, no streaming, no pagination — as the repo's first export
// capability. See design/2026-07-22-green-carbon.md.

// greenFormulaLiteral is quoted verbatim in every export so the computation
// is auditable. It must state exactly what greenFactors.gco2e computes.
const greenFormulaLiteral = "gCO2e = Wh × intensity/1000 × PUE"

type greenMethodologyDTO struct {
	GeneratedAt     string   `json:"generatedAt"`
	PeriodStart     string   `json:"periodStart"`
	PeriodEnd       string   `json:"periodEnd"`
	GridIntensity   float64  `json:"gridIntensity"` // gCO2e/kWh
	IntensitySource string   `json:"intensitySource"`
	PUE             float64  `json:"pue"`
	Formula         string   `json:"formula"`
	Coverage        float64  `json:"coverage"`
	CoverageNote    string   `json:"coverageNote"`
	Metrics         []string `json:"metrics"`
	HubVersion      string   `json:"hubVersion"`
}

type greenReportResponse struct {
	Methodology greenMethodologyDTO `json:"methodology"`
	Totals      greenTotalsDTO      `json:"totals"`
	Services    []greenServiceDTO   `json:"services"`
}

// handleGreenReport produces the export for the requested period as JSON
// (default) or CSV. No topN folding: a report lists everything.
func (a *API) handleGreenReport(w http.ResponseWriter, r *http.Request) error {
	format := r.URL.Query().Get("format")
	switch format {
	case "", "json", "csv":
	default:
		return badRequest("invalid format %q: must be csv or json", format)
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	cfg := a.greenConfig()
	ten := tenant(r)
	rows, err := store.ServiceEnergy(r.Context(), greenQuery(cfg, ten, tr, 0))
	if err != nil {
		return err
	}
	requests, err := a.serviceRequests(r, store, ten, tr)
	if err != nil {
		return err
	}
	f := resolveFactors(cfg)
	services, totals := buildGreenRows(rows, requests, f, 0)
	for i := range services {
		services[i].Points = nil // the report is period totals; trend lives on /summary
	}
	meth := buildMethodology(cfg, f, tr, totals)
	if format == "csv" {
		return writeGreenCSV(w, tr, meth, services)
	}
	writeJSON(w, http.StatusOK, greenReportResponse{Methodology: meth, Totals: totals, Services: services})
	return nil
}

// buildMethodology assembles the audit block. The intensity source label
// carries factor provenance (operator-set vs bundled + dataset vintage)
// verbatim from green.EffectiveIntensity.
func buildMethodology(cfg green.Config, f greenFactors, tr storage.TimeRange, totals greenTotalsDTO) greenMethodologyDTO {
	q := greenQuery(cfg, "", storage.TimeRange{}, 0) // re-defaulted metric names
	metrics := append(append([]string(nil), q.PodEnergyMetrics...), q.NodeEnergyMetrics...)
	note := "no energy measured in the period"
	if total := totals.AttributedWh + totals.UnattributedWh; total > 0 {
		note = fmt.Sprintf(
			"%.1f%% of measured energy was attributed to services; %.3f Wh could not be mapped to a workload and is reported as %s",
			totals.Coverage*100, totals.UnattributedWh, greenUnattributedRow)
	}
	return greenMethodologyDTO{
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		PeriodStart:     tr.Start.UTC().Format(time.RFC3339),
		PeriodEnd:       tr.End.UTC().Format(time.RFC3339),
		GridIntensity:   f.intensity,
		IntensitySource: f.source,
		PUE:             f.pue,
		Formula:         greenFormulaLiteral,
		Coverage:        totals.Coverage,
		CoverageNote:    note,
		Metrics:         metrics,
		HubVersion:      Version,
	}
}

// writeGreenCSV renders the export: `# key,value` methodology lines then the
// data table, all through one csv.Writer so every value stays parseable (the
// bundled intensity source label contains a comma and must be quoted).
func writeGreenCSV(w http.ResponseWriter, tr storage.TimeRange, meth greenMethodologyDTO, services []greenServiceDTO) error {
	filename := fmt.Sprintf("green-report-%s-%s.csv",
		tr.Start.UTC().Format("20060102"), tr.End.UTC().Format("20060102"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// UTF-8 BOM (before the methodology lines) so Excel-on-Windows renders the
	// formula's × as UTF-8 rather than mojibake — standard for spreadsheet CSV.
	if _, err := w.Write([]byte("\ufeff")); err != nil {
		return fmt.Errorf("writing report BOM: %w", err)
	}
	cw := csv.NewWriter(w)
	meta := [][2]string{
		{"generatedAt", meth.GeneratedAt},
		{"periodStart", meth.PeriodStart},
		{"periodEnd", meth.PeriodEnd},
		{"gridIntensity", formatGreenFloat(meth.GridIntensity)},
		{"intensitySource", meth.IntensitySource},
		{"pue", formatGreenFloat(meth.PUE)},
		{"formula", meth.Formula},
		{"coverage", formatGreenFloat(meth.Coverage)},
		{"coverageNote", meth.CoverageNote},
		{"metrics", strings.Join(meth.Metrics, " ")},
		{"hubVersion", meth.HubVersion},
	}
	for _, kv := range meta {
		if err := cw.Write([]string{"# " + kv[0], kv[1]}); err != nil {
			return fmt.Errorf("writing report methodology: %w", err)
		}
	}
	if err := cw.Write([]string{"service", "wh", "gco2e", "requests", "mgCO2ePerRequest"}); err != nil {
		return fmt.Errorf("writing report header: %w", err)
	}
	for _, s := range services {
		requests, mg := "", ""
		if s.Requests > 0 {
			requests = strconv.FormatUint(s.Requests, 10)
			mg = formatGreenFloat(s.MgCO2ePerRequest)
		}
		if err := cw.Write([]string{sanitizeCSVField(s.Service), formatGreenFloat(s.Wh), formatGreenFloat(s.GCO2e), requests, mg}); err != nil {
			return fmt.Errorf("writing report row: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("flushing report: %w", err)
	}
	return nil
}

// formatGreenFloat renders report numbers with three decimals — stable for
// spreadsheets, precise enough to re-derive the formula.
func formatGreenFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}

// sanitizeCSVField neutralizes spreadsheet formula injection. A service name
// is the workload's free-form service.name resource attribute — attacker
// influenceable — and encoding/csv does NOT defuse a leading =, +, -, @ (or a
// leading TAB/CR that a parser may strip to expose the next char): opened in
// Excel/Sheets, `=HYPERLINK(...)`/`=cmd|...` would execute. Prefix such values
// with a single quote (the OWASP guard); the quote is hidden on display yet
// preserved by a CSV reader, so the value still round-trips. Only user-derived
// cells pass through this — numbers and the methodology block are our own.
func sanitizeCSVField(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}
