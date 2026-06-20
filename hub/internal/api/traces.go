package api

import (
	"net/http"

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
	services, err := store.ListServices(r.Context(), storage.ServiceQuery{Tenant: tenant(r), Range: tr})
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

func (a *API) handleTraceOverview(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	ops, err := store.TraceOverview(r.Context(), storage.OverviewQuery{
		Tenant:  tenant(r),
		Range:   tr,
		Service: r.URL.Query().Get("service"),
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

	page, err := store.SearchTraces(r.Context(), storage.TraceQuery{
		Tenant:      tenant(r),
		Range:       tr,
		Service:     r.URL.Query().Get("service"),
		Operation:   r.URL.Query().Get("operation"),
		Status:      status,
		MinDuration: minDur,
		MaxDuration: maxDur,
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
	trace, err := store.GetTrace(r.Context(), tenant(r), traceID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toTraceResponse(trace))
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
	hm, err := store.TraceHeatmap(r.Context(), storage.HeatmapQuery{
		Tenant:          tenant(r),
		Range:           tr,
		Service:         r.URL.Query().Get("service"),
		Operation:       r.URL.Query().Get("operation"),
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
