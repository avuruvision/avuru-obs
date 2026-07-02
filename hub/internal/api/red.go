package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type redPointDTO struct {
	Time       string  `json:"time"`
	RatePerSec float64 `json:"ratePerSec"`
	ErrorRate  float64 `json:"errorRate"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
}

type redSeriesDTO struct {
	Service string        `json:"service"`
	Points  []redPointDTO `json:"points"`
}

type redResponse struct {
	BucketSeconds float64        `json:"bucketSeconds"`
	Series        []redSeriesDTO `json:"series"`
}

// handleREDSeries serves bucketed rate/errors/duration per service (entry
// spans) — the RED metrics dashboard. Without ?service= it returns the
// busiest services (?top=, default backend-side).
func (a *API) handleREDSeries(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	points, err := parseInt(r, "points", 0)
	if err != nil {
		return err
	}
	topN, err := parseInt(r, "top", 0)
	if err != nil {
		return err
	}
	q := storage.REDQuery{
		Tenant:     tenant(r),
		Range:      tr,
		Service:    r.URL.Query().Get("service"),
		Points:     points,
		TopN:       topN,
		ExcludeAux: !parseBool(r, "includeAux", false),
	}
	series, err := store.REDSeries(r.Context(), q)
	if err != nil {
		return err
	}

	// Bucket width mirrors the backend derivation so rates are correct.
	pts := q.Points
	if pts <= 0 {
		pts = 30
	}
	bucket := tr.End.Sub(tr.Start) / time.Duration(pts)
	if bucket < time.Second {
		bucket = time.Second
	}
	bucketSec := bucket.Seconds()

	resp := redResponse{BucketSeconds: bucketSec, Series: make([]redSeriesDTO, 0, len(series))}
	for _, s := range series {
		dto := redSeriesDTO{Service: s.Service, Points: make([]redPointDTO, 0, len(s.Points))}
		for _, p := range s.Points {
			var errRate float64
			if p.Count > 0 {
				errRate = float64(p.ErrorCount) / float64(p.Count)
			}
			dto.Points = append(dto.Points, redPointDTO{
				Time:       p.Time.UTC().Format(time.RFC3339),
				RatePerSec: float64(p.Count) / bucketSec,
				ErrorRate:  errRate,
				P50Ms:      float64(p.P50) / float64(time.Millisecond),
				P95Ms:      float64(p.P95) / float64(time.Millisecond),
				P99Ms:      float64(p.P99) / float64(time.Millisecond),
			})
		}
		resp.Series = append(resp.Series, dto)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
