package api

import (
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func (a *API) handleServices(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	services, err := store.ListServices(r.Context(), storage.ServiceQuery{
		Tenant:     tenant,
		Range:      tr,
		ExcludeAux: !parseBool(r, "includeAux", false),
	})
	if err != nil {
		return err
	}
	resp := servicesResponse{Services: make([]serviceDTO, 0, len(services))}
	for _, s := range services {
		resp.Services = append(resp.Services, toServiceDTO(s, tr.End.Sub(tr.Start)))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleServiceMap returns service nodes (RED metrics) plus derived call edges
// for the topology graph. Auxiliary traffic is excluded by default.
func (a *API) handleServiceMap(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	q := storage.ServiceQuery{
		Tenant:     tenant,
		Range:      tr,
		ExcludeAux: !parseBool(r, "includeAux", false),
	}
	services, err := store.ListServices(r.Context(), q)
	if err != nil {
		return err
	}
	edges, err := store.ServiceEdges(r.Context(), q)
	if err != nil {
		return err
	}
	// Enrich with OBI network-flow edges so services that emit no application
	// traces still appear. The otel_metrics_* tables exist only when the
	// infra-metrics module is active; querying them otherwise would error, so
	// gate the call on the module. When it's off, flowEdges stays nil and
	// mergeEdges just stamps the trace edges with their provenance.
	var flowEdges []storage.ServiceEdge
	var health []storage.NetworkEdgeHealth
	if a.modules.Enabled(modules.InfraMetrics) {
		flowEdges, err = store.NetworkEdges(r.Context(), q)
		if err != nil {
			return err
		}
		health, err = store.NetworkEdgeHealth(r.Context(), q)
		if err != nil {
			return err
		}
	}
	edges = mergeEdges(edges, flowEdges)
	resp := serviceMapResponse{
		Services: make([]serviceDTO, 0, len(services)),
		Edges:    make([]serviceEdgeDTO, 0, len(edges)),
	}
	window := tr.End.Sub(tr.Start)
	for _, s := range services {
		resp.Services = append(resp.Services, toServiceDTO(s, window))
	}
	for _, e := range edges {
		resp.Edges = append(resp.Edges, toServiceEdgeDTO(e))
	}
	resp.Edges = applyEdgeHealth(resp.Edges, health)
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// applyEdgeHealth overlays OBI per-edge connection health (RTT p95, failed
// connections) onto the map edges by (source, target). A health pair with no
// trace/flow edge is appended as a flow edge — TCP stats and flow bytes come
// from the same OBI network feature, so this is rare but kept for completeness.
func applyEdgeHealth(edges []serviceEdgeDTO, health []storage.NetworkEdgeHealth) []serviceEdgeDTO {
	if len(health) == 0 {
		return edges
	}
	type key struct{ src, dst string }
	index := make(map[key]int, len(edges))
	for i, e := range edges {
		index[key{e.Source, e.Target}] = i
	}
	for _, h := range health {
		if i, ok := index[key{h.Source, h.Target}]; ok {
			edges[i].RTTMs = h.RTTMs
			edges[i].FailedConnections = h.FailedConnections
			continue
		}
		edges = append(edges, serviceEdgeDTO{
			Source: h.Source, Target: h.Target, Provenance: "flow",
			RTTMs: h.RTTMs, FailedConnections: h.FailedConnections,
		})
	}
	return edges
}

// mergeEdges folds trace-derived and flow-derived service edges into one set
// for the map. It keys edges on (Source, Target): an edge present in BOTH
// sources becomes "both" — keeping the trace's call volume and taking the
// flow's byte volume — while trace-only edges are "trace" and flow-only edges
// are "flow" (appended after the trace edges). Trace order is preserved so the
// map stays stable across refreshes.
func mergeEdges(traceEdges, flowEdges []storage.ServiceEdge) []storage.ServiceEdge {
	type key struct{ src, dst string }
	merged := make([]storage.ServiceEdge, len(traceEdges))
	index := make(map[key]int, len(traceEdges))
	for i, e := range traceEdges {
		e.Provenance = "trace"
		merged[i] = e
		index[key{e.Source, e.Target}] = i
	}
	for _, fe := range flowEdges {
		if i, ok := index[key{fe.Source, fe.Target}]; ok {
			merged[i].Provenance = "both"
			merged[i].Bytes = fe.Bytes // keep trace Count/ErrorCount, add flow bytes
			continue
		}
		fe.Provenance = "flow"
		merged = append(merged, fe)
	}
	return merged
}

func (a *API) handleTraceOverview(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	ops, err := store.TraceOverview(r.Context(), storage.OverviewQuery{
		Tenant:     tenant,
		Range:      tr,
		Service:    r.URL.Query().Get("service"),
		ExcludeAux: !parseBool(r, "includeAux", false),
	})
	if err != nil {
		return err
	}
	resp := overviewResponse{Operations: make([]operationDTO, 0, len(ops))}
	for _, o := range ops {
		resp.Operations = append(resp.Operations, toOperationDTO(o))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (a *API) handleSearchTraces(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 50)
	if err != nil {
		return err
	}
	minDur, err := parseDurationMs(r, "minDurationMs")
	if err != nil {
		return err
	}
	maxDur, err := parseDurationMs(r, "maxDurationMs")
	if err != nil {
		return err
	}
	cursor, err := parseCursor(r)
	if err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "ok" && status != "error" {
		return badRequest("invalid status: must be ok or error")
	}
	order := r.URL.Query().Get("order")
	switch order {
	case "", "newest", "oldest", "slowest":
	default:
		return badRequest("invalid order: must be newest, oldest or slowest")
	}

	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	page, err := store.SearchTraces(r.Context(), storage.TraceQuery{
		Tenant:      tenant,
		Range:       tr,
		Service:     r.URL.Query().Get("service"),
		Operation:   r.URL.Query().Get("operation"),
		Status:      status,
		Tags:        parseTags(r),
		Order:       order,
		MinDuration: minDur,
		MaxDuration: maxDur,
		ExcludeAux:  !parseBool(r, "includeAux", false),
		Limit:       limit,
		Cursor:      cursor,
	})
	if err != nil {
		return err
	}
	resp := tracesResponse{Traces: make([]traceSummaryDTO, 0, len(page.Traces)), NextCursor: encodeCursor(page.NextCursor)}
	for _, t := range page.Traces {
		resp.Traces = append(resp.Traces, toTraceSummaryDTO(t))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (a *API) handleGetTrace(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	traceID := r.PathValue("traceId")
	if traceID == "" {
		return badRequest("missing traceId")
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	trace, err := store.GetTrace(r.Context(), tenant, traceID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toTraceResponse(trace))
	return nil
}

// handleGetSpan resolves the trace containing a span id (the "paste a span
// id" search). ErrNotFound maps to 404 via handle().
func (a *API) handleGetSpan(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	spanID := r.PathValue("spanId")
	if spanID == "" {
		return badRequest("missing spanId")
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	traceID, err := store.FindSpanTrace(r.Context(), tenant, spanID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, spanLookupResponse{TraceID: traceID, SpanID: spanID})
	return nil
}

func (a *API) handleHeatmap(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	timeBuckets, err := parseInt(r, "timeBuckets", 60)
	if err != nil {
		return err
	}
	durBuckets, err := parseInt(r, "durationBuckets", 24)
	if err != nil {
		return err
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	hm, err := store.TraceHeatmap(r.Context(), storage.HeatmapQuery{
		Tenant:          tenant,
		Range:           tr,
		Service:         r.URL.Query().Get("service"),
		Operation:       r.URL.Query().Get("operation"),
		Tags:            parseTags(r),
		ExcludeAux:      !parseBool(r, "includeAux", false),
		TimeBuckets:     timeBuckets,
		DurationBuckets: durBuckets,
	})
	if err != nil {
		return err
	}
	resp := heatmapResponse{
		StartTime:        tr.Start.UTC(),
		EndTime:          tr.End.UTC(),
		TimeBucketSec:    int(hm.TimeBucket.Seconds()),
		DurationBoundsMs: make([]float64, 0, len(hm.DurationBounds)),
		Cells:            make([]heatmapCellDTO, 0, len(hm.Cells)),
	}
	for _, b := range hm.DurationBounds {
		resp.DurationBoundsMs = append(resp.DurationBoundsMs, ms(b))
	}
	for _, c := range hm.Cells {
		resp.Cells = append(resp.Cells, heatmapCellDTO{T: c.TimeBucket, D: c.DurationBucket, Count: c.Count, ErrorCount: c.ErrorCount})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
