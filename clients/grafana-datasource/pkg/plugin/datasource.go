package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// QueryKind is what a panel is asking for. Deliberately a small closed set:
// the point of the data source is to bring the numbers Avuru Obs already
// computes into a dashboard, not to become a second query language over them.
type QueryKind string

const (
	QueryServices QueryKind = "services"
	QueryHealth   QueryKind = "health"
	QueryTraces   QueryKind = "traces"
	QueryZones    QueryKind = "zones"
)

// queryModel is the per-panel JSON the frontend sends.
type queryModel struct {
	Kind    QueryKind `json:"kind"`
	Service string    `json:"service,omitempty"`
	Status  string    `json:"status,omitempty"`
	Tags    string    `json:"tags,omitempty"`
	Limit   int       `json:"limit,omitempty"`
	Project string    `json:"project,omitempty"`
}

// settings is the data source's own configuration.
type settings struct {
	URL string `json:"url"`
	// Project is the default for panels that do not name one.
	Project string `json:"project,omitempty"`
	// TimeoutSeconds bounds a hub call. A dashboard that hangs is worse than
	// one panel that says it timed out.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

type Datasource struct {
	client   *hubClient
	settings settings
}

var (
	_ backend.QueryDataHandler   = (*Datasource)(nil)
	_ backend.CheckHealthHandler = (*Datasource)(nil)
)

// NewDatasource is the instance factory Grafana calls per data source, and
// again whenever its settings change.
func NewDatasource(_ context.Context, s backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	var cfg settings
	if len(s.JSONData) > 0 {
		if err := json.Unmarshal(s.JSONData, &cfg); err != nil {
			return nil, fmt.Errorf("reading data source settings: %w", err)
		}
	}
	if cfg.URL == "" {
		cfg.URL = s.URL
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("no hub URL configured")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// The token lives in Grafana's encrypted secure settings and is only ever
	// decrypted here, in the backend — it never reaches a browser.
	token := s.DecryptedSecureJSONData["apiToken"]
	return &Datasource{client: newHubClient(cfg.URL, token, timeout), settings: cfg}, nil
}

func (d *Datasource) Dispose() {}

// CheckHealth is the "Save & test" button. It calls the same endpoint every
// other read goes through, so a green check means the credential works — not
// merely that something answered on that host.
func (d *Datasource) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	var caps struct {
		Version string   `json:"version"`
		Modules []string `json:"modules"`
	}
	if err := d.client.get(ctx, "/api/v1/capabilities", nil, d.settings.Project, &caps); err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: err.Error(),
		}, nil
	}
	return &backend.CheckHealthResult{
		Status: backend.HealthStatusOk,
		Message: fmt.Sprintf("Connected to Avuru Obs %s (%d module(s) active)",
			caps.Version, len(caps.Modules)),
	}, nil
}

func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	resp := backend.NewQueryDataResponse()
	for _, q := range req.Queries {
		resp.Responses[q.RefID] = d.query(ctx, q)
	}
	return resp, nil
}

func (d *Datasource) query(ctx context.Context, q backend.DataQuery) backend.DataResponse {
	var m queryModel
	if err := json.Unmarshal(q.JSON, &m); err != nil {
		return errResponse(backend.StatusBadRequest, fmt.Errorf("reading query: %w", err))
	}
	project := m.Project
	if project == "" {
		project = d.settings.Project
	}

	// The panel's time range is the query's time range. A dashboard whose
	// picker did not reach the data would be worse than no dashboard.
	params := url.Values{}
	params.Set("start", q.TimeRange.From.UTC().Format(time.RFC3339))
	params.Set("end", q.TimeRange.To.UTC().Format(time.RFC3339))
	params.Set("windowSec", strconv.Itoa(int(q.TimeRange.Duration().Seconds())))

	switch m.Kind {
	case QueryServices, "":
		return d.queryServices(ctx, params, project)
	case QueryHealth:
		return d.queryHealth(ctx, params, project)
	case QueryTraces:
		return d.queryTraces(ctx, params, project, m)
	case QueryZones:
		return d.queryZones(ctx, params, project)
	default:
		return errResponse(backend.StatusBadRequest,
			fmt.Errorf("unknown query kind %q — expected services, health, traces or zones", m.Kind))
	}
}

func errResponse(status backend.Status, err error) backend.DataResponse {
	return backend.ErrDataResponse(status, err.Error())
}

// statusFor maps a hub failure onto the status Grafana shows. A 401 is the
// user's problem to fix and a 5xx is ours; a dashboard that reports both the
// same way sends people to the wrong place.
func statusFor(err error) backend.Status {
	var ae *apiError
	if ok := asAPIError(err, &ae); ok {
		switch {
		case ae.status == 401 || ae.status == 403:
			return backend.StatusUnauthorized
		case ae.status == 404:
			return backend.StatusNotFound
		case ae.status >= 500:
			return backend.StatusInternal
		}
		return backend.StatusBadRequest
	}
	return backend.StatusInternal
}

func (d *Datasource) queryServices(ctx context.Context, params url.Values, project string) backend.DataResponse {
	var resp struct {
		Services []struct {
			Name       string  `json:"name"`
			SpanCount  uint64  `json:"spanCount"`
			RatePerSec float64 `json:"ratePerSec"`
			ErrorRate  float64 `json:"errorRate"`
			P50Ms      float64 `json:"p50Ms"`
			P95Ms      float64 `json:"p95Ms"`
			P99Ms      float64 `json:"p99Ms"`
		} `json:"services"`
	}
	if err := d.client.get(ctx, "/api/v1/services", params, project, &resp); err != nil {
		return errResponse(statusFor(err), err)
	}
	n := len(resp.Services)
	name := make([]string, n)
	spans := make([]int64, n)
	rate := make([]float64, n)
	errRate := make([]float64, n)
	p50 := make([]float64, n)
	p95 := make([]float64, n)
	p99 := make([]float64, n)
	for i, s := range resp.Services {
		name[i], spans[i] = s.Name, int64(s.SpanCount)
		rate[i], errRate[i] = s.RatePerSec, s.ErrorRate
		p50[i], p95[i], p99[i] = s.P50Ms, s.P95Ms, s.P99Ms
	}
	frame := data.NewFrame("services",
		data.NewField("service", nil, name),
		data.NewField("rate", nil, rate).SetConfig(unit("reqps")),
		data.NewField("errorRate", nil, errRate).SetConfig(unit("percentunit")),
		data.NewField("p50", nil, p50).SetConfig(unit("ms")),
		data.NewField("p95", nil, p95).SetConfig(unit("ms")),
		data.NewField("p99", nil, p99).SetConfig(unit("ms")),
		data.NewField("spans", nil, spans),
	)
	return backend.DataResponse{Frames: data.Frames{frame}}
}

func (d *Datasource) queryHealth(ctx context.Context, params url.Values, project string) backend.DataResponse {
	var resp struct {
		Overall string `json:"overall"`
		Groups  []struct {
			Name        string  `json:"name"`
			Environment string  `json:"environment"`
			Tier        string  `json:"tier"`
			Status      string  `json:"status"`
			RatePerSec  float64 `json:"ratePerSec"`
			ErrorRate   float64 `json:"errorRate"`
			P95Ms       float64 `json:"p95Ms"`
		} `json:"groups"`
	}
	if err := d.client.get(ctx, "/api/v1/health/groups", params, project, &resp); err != nil {
		return errResponse(statusFor(err), err)
	}
	n := len(resp.Groups)
	name := make([]string, n)
	env := make([]string, n)
	tier := make([]string, n)
	status := make([]string, n)
	rate := make([]float64, n)
	errRate := make([]float64, n)
	p95 := make([]float64, n)
	for i, g := range resp.Groups {
		name[i], env[i], tier[i], status[i] = g.Name, g.Environment, g.Tier, g.Status
		rate[i], errRate[i], p95[i] = g.RatePerSec, g.ErrorRate, g.P95Ms
	}
	frame := data.NewFrame("health",
		data.NewField("group", nil, name),
		data.NewField("environment", nil, env),
		data.NewField("tier", nil, tier),
		data.NewField("status", nil, status),
		data.NewField("rate", nil, rate).SetConfig(unit("reqps")),
		data.NewField("errorRate", nil, errRate).SetConfig(unit("percentunit")),
		data.NewField("p95", nil, p95).SetConfig(unit("ms")),
	)
	// The overall rollup is one value the panel would otherwise have to
	// recompute — and recompute differently from the product.
	frame.Meta = &data.FrameMeta{Custom: map[string]any{"overall": resp.Overall}}
	return backend.DataResponse{Frames: data.Frames{frame}}
}

func (d *Datasource) queryTraces(ctx context.Context, params url.Values, project string, m queryModel) backend.DataResponse {
	if m.Service != "" {
		params.Set("service", m.Service)
	}
	if m.Status != "" {
		params.Set("status", m.Status)
	}
	if m.Tags != "" {
		params.Set("tags", m.Tags)
	}
	limit := m.Limit
	if limit <= 0 {
		limit = 50
	}
	params.Set("limit", strconv.Itoa(limit))

	var resp struct {
		Traces []struct {
			TraceID       string  `json:"traceId"`
			RootService   string  `json:"rootService"`
			RootOperation string  `json:"rootOperation"`
			StartTime     string  `json:"startTime"`
			DurationMs    float64 `json:"durationMs"`
			SpanCount     uint64  `json:"spanCount"`
			ErrorCount    uint64  `json:"errorCount"`
		} `json:"traces"`
	}
	if err := d.client.get(ctx, "/api/v1/traces", params, project, &resp); err != nil {
		return errResponse(statusFor(err), err)
	}
	n := len(resp.Traces)
	started := make([]time.Time, n)
	id := make([]string, n)
	svc := make([]string, n)
	op := make([]string, n)
	dur := make([]float64, n)
	spans := make([]int64, n)
	errs := make([]int64, n)
	for i, t := range resp.Traces {
		// A bad timestamp must not lose the row: the trace id is still the
		// useful part, and a zero time is visibly wrong rather than silently
		// absent.
		ts, err := time.Parse(time.RFC3339Nano, t.StartTime)
		if err != nil {
			ts = time.Time{}
		}
		started[i], id[i], svc[i], op[i] = ts, t.TraceID, t.RootService, t.RootOperation
		dur[i], spans[i], errs[i] = t.DurationMs, int64(t.SpanCount), int64(t.ErrorCount)
	}
	frame := data.NewFrame("traces",
		data.NewField("time", nil, started),
		data.NewField("traceId", nil, id),
		data.NewField("service", nil, svc),
		data.NewField("operation", nil, op),
		data.NewField("duration", nil, dur).SetConfig(unit("ms")),
		data.NewField("spans", nil, spans),
		data.NewField("errors", nil, errs),
	)
	return backend.DataResponse{Frames: data.Frames{frame}}
}

func (d *Datasource) queryZones(ctx context.Context, params url.Values, project string) backend.DataResponse {
	var resp struct {
		Zones []struct {
			SrcZone string `json:"srcZone"`
			DstZone string `json:"dstZone"`
			Bytes   uint64 `json:"bytes"`
		} `json:"zones"`
	}
	if err := d.client.get(ctx, "/api/v1/network/zones", params, project, &resp); err != nil {
		return errResponse(statusFor(err), err)
	}
	n := len(resp.Zones)
	src := make([]string, n)
	dst := make([]string, n)
	bytes := make([]int64, n)
	for i, z := range resp.Zones {
		src[i], dst[i], bytes[i] = z.SrcZone, z.DstZone, int64(z.Bytes)
	}
	frame := data.NewFrame("zones",
		data.NewField("srcZone", nil, src),
		data.NewField("dstZone", nil, dst),
		data.NewField("bytes", nil, bytes).SetConfig(unit("bytes")),
	)
	return backend.DataResponse{Frames: data.Frames{frame}}
}

func unit(u string) *data.FieldConfig { return &data.FieldConfig{Unit: u} }
