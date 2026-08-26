package api

import (
	"net/http"
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/topology"
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
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	services, err := store.ListServices(r.Context(), storage.ServiceQuery{
		Tenant:     tenant,
		Tenants:    tenants,
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
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	q := storage.ServiceQuery{
		Tenant:     tenant,
		Tenants:    tenants,
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
	// Classify BEFORE querying the collapsed edges, not after building the
	// response: the ancestry walk needs to know which workloads are proxies in
	// order to step over them. One classifier for the whole response, so the
	// nodes and the edges cannot disagree about what a proxy is.
	cls := a.topologyClassifier().WithEvidence(labelledTransport(services))
	transport := transportNames(cls, services)
	// Recover the app→app dependencies the mesh hides. Free when `transport` is
	// empty — an unmeshed install issues no query and returns the same bytes it
	// did before this existed.
	collapsed, err := store.CollapsedEdges(r.Context(), q, transport)
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
	// Infrastructure that emits no telemetry of its own — databases, caches,
	// brokers — derived from the exit spans of the services calling it. Core:
	// it reads otel_traces, so there is no module to gate on.
	virtual, err := store.VirtualTargets(r.Context(), q)
	if err != nil {
		return err
	}
	edges = mergeEdges(edges, flowEdges)
	edges = mergeCollapsed(edges, collapsed)
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
	// Mark the mesh proxies and gateways. They emit spans and exchange bytes
	// exactly like applications do, so without this the map draws every
	// `app → proxy → app` hop as two application dependencies — a claimed
	// relationship between services that never talk to each other. The hub
	// reports the classification rather than acting on it: dropping the rows
	// here would make the mesh unobservable, and the caller can then choose.
	stampServiceRoles(cls, resp.Services)
	// Energy overlay (module green): per-service Wh/gCO2e as a map lens.
	// Best-effort — the map renders even if the energy read fails.
	if a.modules.Enabled(modules.Green) {
		a.stampServiceEnergy(r.Context(), store, q.Tenant, tr, resp.Services)
	}
	// Namespaces, so the map can draw a boundary around one. Best-effort: a
	// label read that fails costs the boundaries, not the map.
	if labels, lerr := store.ServiceLabels(r.Context(), q); lerr == nil {
		stampServiceNamespaces(labels, resp.Services)
	}
	// Virtual targets go on LAST, after every stamp above: they are neither
	// workloads the topology classifier should re-label, nor pods the energy
	// read can find, nor services with a namespace of their own — a derived
	// dependency lives nowhere. Appending them here keeps all three loops
	// walking real services only.
	resp.Services, resp.Edges = appendVirtualTargets(resp.Services, resp.Edges, virtual, window)
	resp.Edges = applyEdgeHealth(resp.Edges, health)
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// stampServiceNamespaces copies each service's dominant namespace onto its map
// node. k8s.namespace.name first, then service.namespace — the same order the
// health module's auto-grouping uses, so a boundary on the map and a group on
// the health board cannot disagree about where a service lives. A service that
// declares neither keeps an empty namespace: the map then draws it outside every
// boundary, which is the truth, rather than collecting unrelated services into
// an invented bucket.
func stampServiceNamespaces(labels []storage.ServiceLabel, services []serviceDTO) {
	byService := make(map[string]storage.ServiceLabel, len(labels))
	for _, l := range labels {
		byService[l.Service] = l
	}
	for i := range services {
		l := byService[services[i].Name]
		switch {
		case l.K8sNamespace != "":
			services[i].Namespace = l.K8sNamespace
		case l.ServiceNamespace != "":
			services[i].Namespace = l.ServiceNamespace
		}
	}
}

// stampServiceRoles labels each map node with its topology role, leaving
// applications unstamped (the DTO omits the empty role) so a mesh-less install
// gets the same bytes it did before.
func stampServiceRoles(cls topology.Classifier, services []serviceDTO) {
	for i := range services {
		if cls.IsTransport(services[i].Name) {
			services[i].Role = string(topology.RoleTransport)
		}
	}
}

// labelledTransport is the subset of `services` the MESH identified, via the
// labels it writes on its own data plane and the sensor carries on their spans
// (design/2026-08-26-transport-from-labels.md).
//
// Derived from the ListServices rows rather than a query of its own, so the
// evidence describes exactly the services on the map — the two cannot end up
// disagreeing about which window they read.
func labelledTransport(services []storage.ServiceStats) []string {
	var out []string
	for _, s := range services {
		if s.TransportEvidence {
			out = append(out, s.Name)
		}
	}
	return out
}

// transportNames is the classified proxy/gateway set among the services on this
// map, sorted so the query it feeds binds a stable argument. Passing names
// (rather than the glob patterns) keeps the classification in exactly one
// place: hub/internal/topology decides, SQL only matches.
func transportNames(cls topology.Classifier, services []storage.ServiceStats) []string {
	var out []string
	for _, s := range services {
		if cls.IsTransport(s.Name) {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

// mergeCollapsed folds mesh-recovered dependencies into the edge set.
//
// A pair can legitimately have both: some calls routed through the mesh and
// some direct (an exclusion, a port the mesh does not capture). Those are the
// same dependency, so the counts add and `viaTransport` is kept — saying "these
// services do not talk" because part of the traffic took a proxy would be the
// same class of error this feature exists to fix.
//
// Latency comes from whichever path carried more calls. Quantiles over two
// populations cannot be merged after the fact, and the dominant path is the one
// the number actually describes.
func mergeCollapsed(edges, collapsed []storage.ServiceEdge) []storage.ServiceEdge {
	if len(collapsed) == 0 {
		return edges
	}
	type key struct{ src, dst string }
	index := make(map[key]int, len(edges))
	for i, e := range edges {
		index[key{e.Source, e.Target}] = i
	}
	for _, c := range collapsed {
		i, ok := index[key{c.Source, c.Target}]
		if !ok {
			edges = append(edges, c)
			index[key{c.Source, c.Target}] = len(edges) - 1
			continue
		}
		if c.Count > edges[i].Count {
			edges[i].P50, edges[i].P95 = c.P50, c.P95
		}
		edges[i].Count += c.Count
		edges[i].ErrorCount += c.ErrorCount
		edges[i].CollapsedCount += c.CollapsedCount
		edges[i].CollapsedErrors += c.CollapsedErrors
		edges[i].ViaTransport = c.ViaTransport
		// Provenance stays whatever the observed edge already was ("trace",
		// "flow", "both"): the pair IS directly observed, and viaTransport is
		// what records that some of it went the long way.
	}
	return edges
}

// applyEdgeHealth overlays OBI per-edge connection health (RTT p95, failed
// connections, retransmits) onto the map edges by (source, target). A health pair with no
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
			edges[i].Retransmits = h.Retransmits
			continue
		}
		edges = append(edges, serviceEdgeDTO{
			Source: h.Source, Target: h.Target, Provenance: "flow",
			RTTMs: h.RTTMs, FailedConnections: h.FailedConnections, Retransmits: h.Retransmits,
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
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	ops, err := store.TraceOverview(r.Context(), storage.OverviewQuery{
		Tenant:     tenant,
		Tenants:    tenants,
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
	if status != "" && status != "ok" && status != "error" && status != "refused" {
		return badRequest("invalid status: must be ok, refused or error")
	}
	order := r.URL.Query().Get("order")
	switch order {
	case "", "newest", "oldest", "slowest":
	default:
		return badRequest("invalid order: must be newest, oldest or slowest")
	}

	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	page, err := store.SearchTraces(r.Context(), storage.TraceQuery{
		Tenant:      tenant,
		Tenants:     tenants,
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
	_, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	trace, err := store.GetTrace(r.Context(), tenants, traceID)
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
	_, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	traceID, err := store.FindSpanTrace(r.Context(), tenants, spanID)
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
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	hm, err := store.TraceHeatmap(r.Context(), storage.HeatmapQuery{
		Tenant:          tenant,
		Tenants:         tenants,
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
