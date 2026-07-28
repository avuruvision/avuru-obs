//go:build e2e

// Green module e2e against the seeded compose stack: /green/summary computes
// Wh from the seeded Kepler counters and converts with the MOUNTED factor
// config (deploy/compose/green.json: intensity 400 operator-set, PUE 1.2),
// the CSRD report renders its methodology block + data rows, and the seeded
// `payments` budget surfaces. See design/2026-07-22-green-carbon.md.
package e2e

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The seeded Kepler fixture (deploy/compose/seed/fixtures/metrics_kepler.json)
// holds exactly two samples per counter series, 60s apart, rebased by the
// seeder to ~now−5m/now−4m. The hub buckets counters with toStartOfInterval
// (EPOCH-aligned, width = (end−start)/30 for the summary's default interval)
// and computes max−min PER BUCKET — so both samples must land in the SAME
// bucket or every delta reads 0 and the test would be vacuously red. The
// candidate widths below make co-bucketing certain: all are multiples of 60,
// so a width fails only when an epoch multiple of it falls inside the 60s gap
// between the samples, and two widths can only fail TOGETHER when a common
// multiple of both does — lcm(3600,5400,7200,21660,21720) ≈ 44.7 years, whose
// next epoch multiple is in 2059. At least one width therefore co-buckets the
// pair for any present-day seed time; the poll tries each in turn.
var greenBucketSeconds = []int{3600, 5400, 7200, 21660, 21720}

// Mounted compose factors (deploy/compose/green.json) and fixture energy:
// seed-payments 5600−2000 = 3600 J = 1.0 Wh; seed-checkout 1720−1000 = 720 J
// = 0.2 Wh. gCO2e = Wh × 400/1000 × 1.2.
const (
	greenIntensity  = 400.0
	greenPUE        = 1.2
	greenPaymentsWh = 1.0
	greenCheckoutWh = 0.2
)

type greenService struct {
	Service          string  `json:"service"`
	Wh               float64 `json:"wh"`
	GCO2e            float64 `json:"gco2e"`
	Requests         uint64  `json:"requests"`
	MgCO2ePerRequest float64 `json:"mgCO2ePerRequest"`
}

type greenSummary struct {
	Factors struct {
		GridIntensity   float64 `json:"gridIntensity"`
		IntensitySource string  `json:"intensitySource"`
		PUE             float64 `json:"pue"`
		Dataset         string  `json:"dataset"`
	} `json:"factors"`
	Totals struct {
		AttributedWh   float64 `json:"attributedWh"`
		UnattributedWh float64 `json:"unattributedWh"`
		Coverage       float64 `json:"coverage"`
		GCO2e          float64 `json:"gco2e"`
	} `json:"totals"`
	Services []greenService `json:"services"`
}

// greenWindow returns a [now−30·w, now] window: the summary's default
// interval is (end−start)/30, so the bucket width is exactly w seconds.
func greenWindow(w int) (string, string) {
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-time.Duration(30*w) * time.Second)
	return start.Format(time.RFC3339), end.Format(time.RFC3339)
}

// findGreenSummary polls the summary across the candidate bucket widths until
// one shows the seeded payments energy, returning that window and response.
func findGreenSummary(t *testing.T) (start, end string, resp greenSummary) {
	t.Helper()
	poll(t, 60*time.Second, func() error {
		for _, w := range greenBucketSeconds {
			s, e := greenWindow(w)
			q := url.Values{}
			q.Set("start", s)
			q.Set("end", e)
			var r greenSummary
			if err := getJSON("/api/v1/green/summary?"+q.Encode(), &r); err != nil {
				return err
			}
			for _, svc := range r.Services {
				if svc.Service == "seed-payments" && svc.Wh > 0.5 {
					start, end, resp = s, e, r
					return nil
				}
			}
		}
		return fmt.Errorf("seeded payments energy not visible in any candidate window yet")
	})
	return start, end, resp
}

func near(got, want, eps float64) bool { return math.Abs(got-want) <= eps }

// TestGreenSummary proves the whole read path over real ingest: the seeded
// Kepler counters produce per-service Wh via the counter-delta SQL and the
// kubeletstats pod→workload join, and carbon derives from the MOUNTED config
// — factors echo the compose green.json, never a built-in default.
func TestGreenSummary(t *testing.T) {
	_, _, resp := findGreenSummary(t)

	f := resp.Factors
	if f.GridIntensity != greenIntensity || f.PUE != greenPUE {
		t.Fatalf("factors = %+v, want mounted green.json intensity %v / pue %v", f, greenIntensity, greenPUE)
	}
	if f.IntensitySource != "operator-set" {
		t.Errorf("intensitySource = %q, want operator-set (gridIntensity is explicit in green.json)", f.IntensitySource)
	}

	byName := map[string]greenService{}
	for _, s := range resp.Services {
		byName[s.Service] = s
	}
	pay, ok := byName["seed-payments"]
	if !ok {
		t.Fatalf("seed-payments missing from services: %+v", resp.Services)
	}
	if !near(pay.Wh, greenPaymentsWh, 0.01) {
		t.Errorf("seed-payments Wh = %v, want %v (fixture Δ3600 J)", pay.Wh, greenPaymentsWh)
	}
	wantG := greenPaymentsWh * greenIntensity / 1000 * greenPUE // 0.48
	if !near(pay.GCO2e, wantG, 0.005) {
		t.Errorf("seed-payments gCO2e = %v, want %v (Wh × intensity/1000 × PUE)", pay.GCO2e, wantG)
	}

	co, ok := byName["seed-checkout"]
	if !ok {
		t.Fatalf("seed-checkout missing from services: %+v", resp.Services)
	}
	if !near(co.Wh, greenCheckoutWh, 0.01) {
		t.Errorf("seed-checkout Wh = %v, want %v (fixture Δ720 J)", co.Wh, greenCheckoutWh)
	}

	// Both seeded pods resolve through the kubeletstats join, so everything is
	// attributed: coverage 1.0 and no unattributed row.
	if !near(resp.Totals.AttributedWh, greenPaymentsWh+greenCheckoutWh, 0.02) {
		t.Errorf("attributedWh = %v, want %v", resp.Totals.AttributedWh, greenPaymentsWh+greenCheckoutWh)
	}
	if resp.Totals.Coverage < 0.999 {
		t.Errorf("coverage = %v, want 1.0 (both seeded pods map to workloads)", resp.Totals.Coverage)
	}
	if _, ok := byName["(unattributed)"]; ok {
		t.Errorf("unexpected (unattributed) row: %+v", resp.Services)
	}
}

// TestGreenReportCSV: the CSRD export renders `# key,value` methodology lines
// (formula, factor provenance, coverage, metric names) followed by the data
// table, over the same window the summary proved.
func TestGreenReportCSV(t *testing.T) {
	start, end, _ := findGreenSummary(t)

	q := url.Values{}
	q.Set("format", "csv")
	q.Set("start", start)
	q.Set("end", end)
	httpResp, err := http.Get(hubURL + "/api/v1/green/report?" + q.Encode())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("report status = %s", httpResp.Status)
	}
	if ct := httpResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		t.Fatalf("reading report: %v", err)
	}
	body := strings.TrimPrefix(string(raw), "\ufeff") // Excel BOM

	for _, want := range []string{
		"# formula,gCO2e = Wh × intensity/1000 × PUE",
		"# gridIntensity,400.000",
		"# pue,1.200",
		"# intensitySource,operator-set",
		"# metrics,kepler_pod_cpu_joules_total kepler_node_cpu_joules_total",
		"service,wh,gco2e,requests,mgCO2ePerRequest",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("report missing line %q\nbody:\n%s", want, body)
		}
	}

	// Data rows: the two seeded services with the exact energy/carbon numbers
	// (period totals; requests join varies with sibling-test traffic, so only
	// the leading service,wh,gco2e cells are pinned).
	for _, prefix := range []string{
		"seed-payments,1.000,0.480,",
		"seed-checkout,0.200,0.096,",
	} {
		if !strings.Contains(body, prefix) {
			t.Errorf("report missing data row %q\nbody:\n%s", prefix, body)
		}
	}
}

// TestGreenBudgets: the mounted compose green.json defines one monthly budget
// over the seeded `payments` service-health group — it must surface with its
// config numbers and a valid status. (Month-to-date usage depends on where in
// the calendar month the run lands, so the amount itself is not pinned.)
func TestGreenBudgets(t *testing.T) {
	type budget struct {
		Name            string  `json:"name"`
		Group           string  `json:"group"`
		MonthlyKgCO2e   float64 `json:"monthlyKgCO2e"`
		UsedKgCO2e      float64 `json:"usedKgCO2e"`
		ProjectedKgCO2e float64 `json:"projectedKgCO2e"`
		Ratio           float64 `json:"ratio"`
		Status          string  `json:"status"`
	}
	var resp struct {
		Window struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"window"`
		Budgets []budget `json:"budgets"`
	}
	poll(t, 30*time.Second, func() error {
		if err := getJSON("/api/v1/green/budgets", &resp); err != nil {
			return err
		}
		if len(resp.Budgets) == 0 {
			return fmt.Errorf("no budgets yet (green.json not loaded?)")
		}
		return nil
	})

	var pay *budget
	for i := range resp.Budgets {
		if resp.Budgets[i].Name == "payments-monthly" {
			pay = &resp.Budgets[i]
		}
	}
	if pay == nil {
		t.Fatalf("payments-monthly budget missing: %+v", resp.Budgets)
	}
	if pay.Group != "payments" || pay.MonthlyKgCO2e != 10 {
		t.Errorf("budget config mismatch: %+v, want group=payments monthlyKgCO2e=10", pay)
	}
	switch pay.Status {
	case "ok", "warn", "exceeded":
	default:
		t.Errorf("budget status = %q, want ok|warn|exceeded", pay.Status)
	}
	if pay.UsedKgCO2e < 0 || pay.ProjectedKgCO2e < pay.UsedKgCO2e-1e-9 {
		t.Errorf("month-to-date math off: used=%v projected=%v", pay.UsedKgCO2e, pay.ProjectedKgCO2e)
	}
	// The window is the current UTC calendar month, budgets being monthly by
	// definition — a day-01 00:00 start regardless of query params.
	if !strings.Contains(resp.Window.Start, "-01T00:00:00") {
		t.Errorf("budgets window start = %q, want the first of the UTC month", resp.Window.Start)
	}
}
