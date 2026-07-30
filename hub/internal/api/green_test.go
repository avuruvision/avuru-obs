package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// greenMux wires a fake store and a static green config into the router
// (zero-value Modules = all modules on, green included).
func greenMux(fake *storagetest.Fake, cfg green.Config) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		GreenConfig: func() green.Config { return cfg },
	})
	return mux
}

// testGreenConfig: intensity 500 gCO2e/kWh × PUE 2 → gCO2e == Wh, so expected
// values read directly off the Wh inputs.
func testGreenConfig() green.Config {
	return green.Config{GridIntensity: 500, PUE: 2}
}

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestGreenConversionMath(t *testing.T) {
	tests := []struct {
		name string
		cfg  green.Config
		wh   float64
		want float64
	}{
		{"operator factors", green.Config{GridIntensity: 500, PUE: 2}, 10, 10},
		// Direct-constructed zero config re-defaults: world 480, PUE 1.5.
		{"world average default PUE", green.Config{}, 1000, 1000 * 480.0 / 1000 * 1.5},
		{"bundled region", green.Config{Region: "FR", PUE: 1.2}, 100, 100 * 50.0 / 1000 * 1.2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := resolveFactors(tt.cfg)
			if got := f.gco2e(tt.wh); !almost(got, tt.want) {
				t.Errorf("gco2e(%v) = %v, want %v", tt.wh, got, tt.want)
			}
		})
	}
}

func TestGreenSummary(t *testing.T) {
	day := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	fake := &storagetest.Fake{
		ServiceEnergies: []storage.ServiceEnergy{
			{Service: "checkout", WattHours: 10, Points: []storage.EnergyPoint{
				{Time: day, WattHours: 4}, {Time: day.Add(time.Hour), WattHours: 6},
			}},
			{Service: "batch", WattHours: 5},
			{Service: "", WattHours: 5}, // unattributed bucket
		},
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 1000}},
	}
	mux := greenMux(fake, testGreenConfig())

	rec := get(t, mux, "/api/v1/green/summary")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp greenSummaryResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Factors block quotes the effective intensity and its provenance, plus the
	// bundled-dataset label (present even when the operator overrode the factor).
	if resp.Factors.GridIntensity != 500 || resp.Factors.PUE != 2 || resp.Factors.IntensitySource != "operator-set" {
		t.Errorf("factors = %+v", resp.Factors)
	}
	if resp.Factors.Dataset != green.IntensityDataset || resp.Factors.Dataset == "" {
		t.Errorf("factors dataset = %q, want %q", resp.Factors.Dataset, green.IntensityDataset)
	}
	// Totals: 15 Wh attributed, 5 unattributed, coverage 0.75, 20 gCO2e.
	tot := resp.Totals
	if tot.AttributedWh != 15 || tot.UnattributedWh != 5 || !almost(tot.Coverage, 0.75) || !almost(tot.GCO2e, 20) {
		t.Errorf("totals = %+v", tot)
	}
	if len(resp.Services) != 3 {
		t.Fatalf("services = %+v, want 3 rows", resp.Services)
	}
	checkout := resp.Services[0]
	if checkout.Service != "checkout" || checkout.Wh != 10 || !almost(checkout.GCO2e, 10) {
		t.Errorf("checkout row = %+v", checkout)
	}
	// Per-request join: 10 gCO2e over 1000 requests = 10 mg/request.
	if checkout.Requests != 1000 || !almost(checkout.MgCO2ePerRequest, 10) {
		t.Errorf("checkout per-request = %+v", checkout)
	}
	if len(checkout.Points) != 2 || checkout.Points[0].Wh != 4 || checkout.Points[0].Time != "2026-07-10T00:00:00Z" {
		t.Errorf("checkout trend = %+v", checkout.Points)
	}
	// batch has energy but no requests: the per-request ratio is omitted.
	if batch := resp.Services[1]; batch.Requests != 0 || batch.MgCO2ePerRequest != 0 {
		t.Errorf("batch row = %+v", batch)
	}
	// The unattributed bucket is its own named row, last.
	if unattr := resp.Services[2]; unattr.Service != greenUnattributedRow || unattr.Wh != 5 || !almost(unattr.GCO2e, 5) {
		t.Errorf("unattributed row = %+v", unattr)
	}
	// The storage query carries the config's (re-defaulted) metric names —
	// nothing outside the config may hardcode Kepler naming.
	q := fake.LastGreenQuery
	if len(q.PodEnergyMetrics) != 1 || q.PodEnergyMetrics[0] != "kepler_pod_cpu_joules_total" ||
		len(q.NodeEnergyMetrics) != 1 || q.NodeEnergyMetrics[0] != "kepler_node_cpu_joules_total" ||
		q.PodNameAttr != "pod_name" || q.PodNamespaceAttr != "pod_namespace" {
		t.Errorf("green query = %+v", q)
	}
	if q.Tenant != storage.DefaultTenant {
		t.Errorf("tenant = %q, want default", q.Tenant)
	}
}

func TestGreenSummaryTopNFold(t *testing.T) {
	fake := &storagetest.Fake{
		ServiceEnergies: []storage.ServiceEnergy{
			{Service: "a", WattHours: 10},
			{Service: "b", WattHours: 6},
			{Service: "c", WattHours: 3},
			{Service: "d", WattHours: 1},
			{Service: "", WattHours: 5},
		},
		Services: []storage.ServiceStats{{Name: "c", SpanCount: 100}, {Name: "d", SpanCount: 100}},
	}
	mux := greenMux(fake, testGreenConfig())

	rec := get(t, mux, "/api/v1/green/summary?topN=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp greenSummaryResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := make([]string, 0, len(resp.Services))
	for _, s := range resp.Services {
		names = append(names, s.Service)
	}
	want := []string{"a", "b", greenOtherRow, greenUnattributedRow}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", names, want)
	}
	// (other) folds the tail: c+d = 4 Wh, 200 requests, 4 gCO2e → 20 mg/req.
	other := resp.Services[2]
	if other.Wh != 4 || !almost(other.GCO2e, 4) || other.Requests != 200 || !almost(other.MgCO2ePerRequest, 20) {
		t.Errorf("(other) row = %+v", other)
	}
	// The unattributed bucket is never folded into (other).
	if unattr := resp.Services[3]; unattr.Wh != 5 {
		t.Errorf("unattributed row = %+v", unattr)
	}
	// Totals ignore the fold.
	if resp.Totals.AttributedWh != 20 || resp.Totals.UnattributedWh != 5 {
		t.Errorf("totals = %+v", resp.Totals)
	}
}

func TestGreenSummaryBadParams(t *testing.T) {
	mux := greenMux(&storagetest.Fake{}, testGreenConfig())
	for _, path := range []string{
		"/api/v1/green/summary?topN=-1",
		"/api/v1/green/summary?topN=abc",
		"/api/v1/green/summary?start=garbage",
	} {
		if rec := get(t, mux, path); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", path, rec.Code)
		}
	}
}

func TestGreenSummaryStorageError(t *testing.T) {
	fake := &storagetest.Fake{GreenErr: errors.New("boom")}
	mux := greenMux(fake, testGreenConfig())
	if rec := get(t, mux, "/api/v1/green/summary"); rec.Code != http.StatusInternalServerError {
		t.Errorf("summary with storage error: got %d, want 500", rec.Code)
	}
}

func TestGreenBudgetsStatusAndProjection(t *testing.T) {
	// Fixed clock: July 16 12:00 UTC — exactly half of the 31-day month, so
	// the linear projection is used × 2.
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	day1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	rows := []storage.ServiceEnergy{
		{Service: "checkout", WattHours: 1000, Points: []storage.EnergyPoint{
			{Time: day1, WattHours: 400},
			{Time: day1.AddDate(0, 0, 1), WattHours: 600},
		}},
		{Service: "batch", WattHours: 500}, // energy-only: NOT in the RED stats
		{Service: "", WattHours: 50},       // unattributed never counts toward budgets
	}
	stats := []storage.ServiceStats{{Name: "checkout", SpanCount: 10}}
	labels := []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}}
	cfg := green.Config{
		GridIntensity: 500, PUE: 2, // 1000 Wh → 1000 gCO2e = 1 kg
		Budgets: []green.Budget{
			{Name: "ok", Group: "shop", MonthlyKgCO2e: 10, WarnRatio: 0.8},
			{Name: "warn", Group: "shop", MonthlyKgCO2e: 1.2, WarnRatio: 0.8},
			{Name: "over", Group: "shop", MonthlyKgCO2e: 0.9, WarnRatio: 0.8},
			// A budget whose group matches no live service must still appear.
			{Name: "idle", Group: "empty-team", MonthlyKgCO2e: 5, WarnRatio: 0.8},
			// batch has energy but no RED stats: it auto-groups by namespace
			// ((unlabeled) — no labels) and must still burn this budget.
			{Name: "batch-team", Group: "(unlabeled)", MonthlyKgCO2e: 1, WarnRatio: 0.8},
		},
	}
	budgets := buildGreenBudgets(cfg, health.Default(), resolveFactors(cfg), rows, stats, labels, now)
	if len(budgets) != 5 {
		t.Fatalf("budgets = %+v, want 5", budgets)
	}
	by := map[string]greenBudgetDTO{}
	for _, b := range budgets {
		by[b.Name] = b
	}

	ok := by["ok"]
	if !almost(ok.UsedKgCO2e, 1) || !almost(ok.Ratio, 0.1) || ok.Status != "ok" {
		t.Errorf("ok budget = %+v", ok)
	}
	if !almost(ok.ProjectedKgCO2e, 2) {
		t.Errorf("projection = %v, want 2 (used/half-month)", ok.ProjectedKgCO2e)
	}
	// Burn-down: cumulative daily kg — 0.4 then 1.0.
	if len(ok.BurnDown) != 2 || !almost(ok.BurnDown[0].KgCO2e, 0.4) || !almost(ok.BurnDown[1].KgCO2e, 1.0) {
		t.Errorf("burn-down = %+v", ok.BurnDown)
	}
	if ok.BurnDown[0].Time != "2026-07-01T00:00:00Z" {
		t.Errorf("burn-down time = %q", ok.BurnDown[0].Time)
	}

	if warn := by["warn"]; warn.Status != "warn" || !almost(warn.Ratio, 1/1.2) {
		t.Errorf("warn budget = %+v", warn)
	}
	if over := by["over"]; over.Status != "exceeded" {
		t.Errorf("over budget = %+v", over)
	}
	if idle := by["idle"]; idle.UsedKgCO2e != 0 || idle.Status != "ok" || len(idle.BurnDown) != 0 {
		t.Errorf("idle budget = %+v", idle)
	}
	if batch := by["batch-team"]; !almost(batch.UsedKgCO2e, 0.5) || batch.Status != "ok" {
		t.Errorf("batch-team budget = %+v (energy-only service must roll up)", batch)
	}
}

func TestGreenBudgetsMonthWindow(t *testing.T) {
	fake := &storagetest.Fake{}
	cfg := testGreenConfig()
	cfg.Budgets = []green.Budget{{Name: "team", Group: "shop", MonthlyKgCO2e: 5, WarnRatio: 0.8}}
	mux := greenMux(fake, cfg)

	// Query params must NOT move the window: budgets are calendar-month UTC.
	rec := get(t, mux, "/api/v1/green/budgets?start=2020-01-01T00:00:00Z&end=2020-01-02T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	q := fake.LastGreenQuery
	wantStart := time.Date(q.Range.End.Year(), q.Range.End.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !q.Range.Start.Equal(wantStart) {
		t.Errorf("window start = %v, want month start %v", q.Range.Start, wantStart)
	}
	if q.Interval != 24*time.Hour {
		t.Errorf("interval = %v, want 24h (daily burn-down buckets)", q.Interval)
	}
	var resp greenBudgetsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Budgets) != 1 || resp.Budgets[0].Name != "team" || resp.Budgets[0].Status != "ok" {
		t.Errorf("budgets = %+v", resp.Budgets)
	}
}

func TestGreenReportJSON(t *testing.T) {
	day := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	fake := &storagetest.Fake{
		ServiceEnergies: []storage.ServiceEnergy{
			{Service: "checkout", WattHours: 10, Points: []storage.EnergyPoint{{Time: day, WattHours: 10}}},
			{Service: "", WattHours: 10},
		},
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 100}},
	}
	// Bundled region factor: FR 50 gCO2e/kWh, default PUE 1.5.
	mux := greenMux(fake, green.Config{Region: "FR"})

	rec := get(t, mux, "/api/v1/green/report?start=2026-07-01T00:00:00Z&end=2026-07-21T00:00:00Z&format=json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp greenReportResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	m := resp.Methodology
	if m.Formula != greenFormulaLiteral {
		t.Errorf("formula = %q, want the literal", m.Formula)
	}
	if m.GridIntensity != 50 || m.PUE != 1.5 {
		t.Errorf("factors = %+v", m)
	}
	if !strings.Contains(m.IntensitySource, "FR") || !strings.Contains(m.IntensitySource, "Ember") {
		t.Errorf("intensity source = %q, want bundled FR provenance", m.IntensitySource)
	}
	if m.PeriodStart != "2026-07-01T00:00:00Z" || m.PeriodEnd != "2026-07-21T00:00:00Z" {
		t.Errorf("period = %q..%q", m.PeriodStart, m.PeriodEnd)
	}
	if !almost(m.Coverage, 0.5) || !strings.Contains(m.CoverageNote, greenUnattributedRow) {
		t.Errorf("coverage = %v note = %q", m.Coverage, m.CoverageNote)
	}
	if strings.Join(m.Metrics, " ") != "kepler_pod_cpu_joules_total kepler_node_cpu_joules_total" {
		t.Errorf("metrics = %v", m.Metrics)
	}
	if m.HubVersion != Version || m.HubVersion == "" {
		t.Errorf("hub version = %q, want %q", m.HubVersion, Version)
	}
	if _, err := time.Parse(time.RFC3339, m.GeneratedAt); err != nil {
		t.Errorf("generatedAt = %q: %v", m.GeneratedAt, err)
	}
	// The report is period totals: no trend series on its rows.
	if len(resp.Services) != 2 || resp.Services[0].Points != nil {
		t.Errorf("report rows = %+v", resp.Services)
	}
}

func TestGreenReportCSV(t *testing.T) {
	fake := &storagetest.Fake{
		ServiceEnergies: []storage.ServiceEnergy{
			{Service: "checkout", WattHours: 10},
			{Service: "", WattHours: 5},
		},
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 1000}},
	}
	// Region DE: the bundled source label contains a comma — the CSV must
	// quote it and stay parseable end to end.
	mux := greenMux(fake, green.Config{Region: "DE"})

	rec := get(t, mux, "/api/v1/green/report?format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="green-report-`) {
		t.Errorf("content-disposition = %q", cd)
	}

	records := readGreenCSV(t, rec.Body.Bytes())
	meta := map[string]string{}
	var table [][]string
	for _, r := range records {
		if strings.HasPrefix(r[0], "# ") {
			if len(r) != 2 {
				t.Fatalf("methodology line %v: want 2 fields", r)
			}
			meta[strings.TrimPrefix(r[0], "# ")] = r[1]
			continue
		}
		table = append(table, r)
	}
	if meta["formula"] != greenFormulaLiteral {
		t.Errorf("formula = %q", meta["formula"])
	}
	if !strings.Contains(meta["intensitySource"], "DE, Ember") {
		t.Errorf("intensity source = %q (comma must survive quoting)", meta["intensitySource"])
	}
	if meta["gridIntensity"] != "380.000" || meta["pue"] != "1.500" {
		t.Errorf("factors = %q / %q", meta["gridIntensity"], meta["pue"])
	}
	if meta["hubVersion"] == "" || meta["generatedAt"] == "" || meta["coverageNote"] == "" {
		t.Errorf("methodology incomplete: %v", meta)
	}
	// Data table: header + checkout + (unattributed).
	if len(table) != 3 {
		t.Fatalf("table = %v, want header + 2 rows", table)
	}
	if strings.Join(table[0], ",") != "service,wh,gco2e,requests,mgCO2ePerRequest" {
		t.Errorf("header = %v", table[0])
	}
	// DE 380 × PUE 1.5: 10 Wh → 5.7 gCO2e; 1000 requests → 5.7 mg/request.
	if strings.Join(table[1], "|") != "checkout|10.000|5.700|1000|5.700" {
		t.Errorf("checkout row = %v", table[1])
	}
	// Unattributed: no requests → empty request columns, not zeros.
	if strings.Join(table[2], "|") != "(unattributed)|5.000|2.850||" {
		t.Errorf("unattributed row = %v", table[2])
	}
}

// readGreenCSV strips the UTF-8 BOM the export prepends (Excel-facing) and
// parses the rest; methodology lines carry 2 fields, the data table 5.
func readGreenCSV(t *testing.T, body []byte) [][]string {
	t.Helper()
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte("\ufeff"))))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	return records
}

func TestGreenReportCSVInjectionGuard(t *testing.T) {
	fake := &storagetest.Fake{
		ServiceEnergies: []storage.ServiceEnergy{
			{Service: "=1+2", WattHours: 10},    // formula
			{Service: "@evil", WattHours: 5},    // formula
			{Service: "-cmd", WattHours: 1},     // formula
			{Service: "\ttabbed", WattHours: 1}, // leading TAB
			{Service: "safe", WattHours: 2},     // benign — untouched
		},
	}
	mux := greenMux(fake, testGreenConfig())

	rec := get(t, mux, "/api/v1/green/report?format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	seen := map[string]bool{}
	for _, r := range readGreenCSV(t, rec.Body.Bytes()) {
		seen[r[0]] = true
	}
	// Risky-leading service names round-trip through the CSV reader with the
	// single-quote guard so a spreadsheet treats them as text, not live
	// formulas; a benign name is left untouched.
	for _, want := range []string{"'=1+2", "'@evil", "'-cmd", "'\ttabbed", "safe"} {
		if !seen[want] {
			t.Errorf("row %q missing (injection guard); got %v", want, seen)
		}
	}
}

func TestServiceMapEnergyStamping(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 5},
			{Name: "web", SpanCount: 2},
		},
		ServiceEnergies: []storage.ServiceEnergy{
			{Service: "checkout", WattHours: 10},
			{Service: "", WattHours: 3}, // unattributed is never stamped on a node
		},
	}
	mux := greenMux(fake, testGreenConfig())

	rec := get(t, mux, "/api/v1/service-map")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	var resp serviceMapResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]serviceDTO{}
	for _, s := range resp.Services {
		byName[s.Name] = s
	}
	if c := byName["checkout"]; c.Wh != 10 || !almost(c.GCO2e, 10) {
		t.Errorf("checkout = %+v, want wh=10 gco2e=10", c)
	}
	// web has no energy: its row must omit the keys entirely (omitempty).
	if w := byName["web"]; w.Wh != 0 || w.GCO2e != 0 {
		t.Errorf("web = %+v, want no energy", w)
	}
	if got := strings.Count(body, `"wh"`); got != 1 {
		t.Errorf(`"wh" appears %d times, want 1 (omitempty on the energy-less row)`, got)
	}
}

func TestServiceMapNoEnergyWhenGreenOff(t *testing.T) {
	fake := &storagetest.Fake{
		Services:        []storage.ServiceStats{{Name: "checkout", SpanCount: 5}},
		ServiceEnergies: []storage.ServiceEnergy{{Service: "checkout", WattHours: 10}},
	}
	set, err := modules.Parse("core,infra-metrics")
	if err != nil {
		t.Fatalf("parse modules: %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{Modules: set})

	rec := get(t, mux, "/api/v1/service-map")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	// Non-green installs keep a byte-identical wire shape: no energy keys.
	if body := rec.Body.String(); strings.Contains(body, `"wh"`) || strings.Contains(body, `"gco2e"`) {
		t.Errorf("energy keys leaked with green off: %s", body)
	}
	// The energy query must not even run (metrics tables may be absent).
	if !fake.LastGreenQuery.Range.Start.IsZero() {
		t.Errorf("ServiceEnergy was queried with green off: %+v", fake.LastGreenQuery)
	}
}

func TestServiceMapSurvivesEnergyError(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 5}},
		GreenErr: errors.New("boom"),
	}
	mux := greenMux(fake, testGreenConfig())

	rec := get(t, mux, "/api/v1/service-map")
	if rec.Code != http.StatusOK {
		t.Fatalf("map must survive an energy error: status %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, `"wh"`) {
		t.Errorf("energy keys present despite storage error: %s", body)
	}
}

func TestBuildGreenRows_QualitySplit(t *testing.T) {
	rows := []storage.ServiceEnergy{
		{Service: "web", Quality: "measured", WattHours: 10},
		{Service: "web", Quality: "estimated", WattHours: 5},
	}
	f := greenFactors{intensity: 480, pue: 1.5}
	services, totals := buildGreenRows(rows, nil, f, 0)

	if len(services) != 1 {
		t.Fatalf("len(services) = %d, want 1 (one merged row per service)", len(services))
	}
	if !almost(services[0].Wh, 15) {
		t.Errorf("services[0].Wh = %v, want 15 (measured+estimated summed for the total)", services[0].Wh)
	}
	if !almost(services[0].EstimatedWh, 5) {
		t.Errorf("services[0].EstimatedWh = %v, want 5", services[0].EstimatedWh)
	}
	if !almost(totals.MeasuredWh, 10) || !almost(totals.EstimatedWh, 5) {
		t.Errorf("totals = %+v, want MeasuredWh=10 EstimatedWh=5", totals)
	}
	if !almost(totals.AttributedWh, 15) {
		t.Errorf("totals.AttributedWh = %v, want 15 (never silently blended, but the total IS the sum)", totals.AttributedWh)
	}
}

func TestBuildGreenBudgets_EstimatedShare(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	rows := []storage.ServiceEnergy{
		{Service: "web", Quality: "measured", WattHours: 1000},
		{Service: "web", Quality: "estimated", WattHours: 3000},
	}
	stats := []storage.ServiceStats{{Name: "web", SpanCount: 10}}
	labels := []storage.ServiceLabel{{Service: "web", K8sNamespace: "shop"}}
	cfg := green.Config{
		GridIntensity: 500, PUE: 2, // gCO2e == Wh with these factors
		Budgets: []green.Budget{{Name: "prod", Group: "shop", MonthlyKgCO2e: 100, WarnRatio: 0.8}},
	}

	budgets := buildGreenBudgets(cfg, health.Default(), resolveFactors(cfg), rows, stats, labels, now)
	if len(budgets) != 1 {
		t.Fatalf("len(budgets) = %d, want 1", len(budgets))
	}
	b := budgets[0]
	// Total used includes BOTH tiers (an all-VM fleet must be able to trip a
	// budget — AEP explicit goal): (1000+3000) gCO2e / 1000 = 4 kg.
	if !almost(b.UsedKgCO2e, 4) {
		t.Errorf("UsedKgCO2e = %v, want 4", b.UsedKgCO2e)
	}
	// 3000 of the 4000 Wh (75%) is estimated -> EstimatedShare = 0.75.
	if !almost(b.EstimatedShare, 0.75) {
		t.Errorf("EstimatedShare = %v, want 0.75", b.EstimatedShare)
	}
}

func TestBuildMethodology_EstimationSubsection(t *testing.T) {
	cfg := green.Default()
	f := greenFactors{intensity: 480, pue: 1.5}
	totals := greenTotalsDTO{AttributedWh: 100, MeasuredWh: 40, EstimatedWh: 60, Coverage: 1}
	tr := storage.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()}

	meth := buildMethodology(cfg, f, tr, totals)
	if meth.Estimation == nil {
		t.Fatal("Estimation subsection is nil, want populated (totals.EstimatedWh > 0)")
	}
	if meth.Estimation.FormulaLiteral == "" || meth.Estimation.ErrorBand == "" {
		t.Errorf("Estimation = %+v, want non-empty formula and error band", meth.Estimation)
	}
	if meth.Estimation.CoefficientDataset == "" {
		t.Errorf("Estimation.CoefficientDataset is empty, want the bundled dataset label")
	}
}

func TestBuildMethodology_NoEstimationSubsectionWhenFullyMeasured(t *testing.T) {
	cfg := green.Default()
	f := greenFactors{intensity: 480, pue: 1.5}
	totals := greenTotalsDTO{AttributedWh: 100, MeasuredWh: 100, EstimatedWh: 0, Coverage: 1}
	tr := storage.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()}

	meth := buildMethodology(cfg, f, tr, totals)
	if meth.Estimation != nil {
		t.Errorf("Estimation = %+v, want nil (report stays unchanged for fully-measured installs)", meth.Estimation)
	}
}
