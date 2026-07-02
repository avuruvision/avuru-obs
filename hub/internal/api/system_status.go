package api

import (
	"fmt"
	"net/http"
	"time"
)

// ingestionFreshWindow is how recent the newest data must be for ingestion to
// count as "healthy" (otherwise it's reported idle — likely just no traffic).
const ingestionFreshWindow = 5 * time.Minute

type componentHealth struct {
	Name   string `json:"name"`
	Status string `json:"status"` // healthy | down | idle | unknown
	Detail string `json:"detail,omitempty"`
}

type signalStatsDTO struct {
	Signal          string  `json:"signal"`
	Rows            uint64  `json:"rows"`
	Bytes           uint64  `json:"bytes"`
	CompressedBytes uint64  `json:"compressedBytes"`
	Compression     float64 `json:"compression"`
	Oldest          *string `json:"oldest,omitempty"`
	Newest          *string `json:"newest,omitempty"`
	RetentionDays   int     `json:"retentionDays"`
}

type diskDTO struct {
	Name       string `json:"name"`
	FreeBytes  uint64 `json:"freeBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

type systemStatusResponse struct {
	Version    string            `json:"version"`
	Overall    string            `json:"overall"` // healthy | degraded | down
	CheckedAt  string            `json:"checkedAt"`
	Components []componentHealth `json:"components"`
	Signals    []signalStatsDTO  `json:"signals"`
	Disks      []diskDTO         `json:"disks"`
}

// handleSystemStatus reports overall backend health for the Settings → Status
// view. It always answers 200 so the page renders even when ClickHouse is down.
func (a *API) handleSystemStatus(w http.ResponseWriter, r *http.Request) error {
	now := time.Now().UTC()
	resp := systemStatusResponse{
		Version:   Version,
		CheckedAt: now.Format(time.RFC3339),
		Components: []componentHealth{
			{Name: "Hub", Status: "healthy", Detail: Version},
		},
	}

	store := a.provider()
	if store == nil || store.Ping(r.Context()) != nil {
		resp.Overall = "down"
		resp.Components = append(resp.Components,
			componentHealth{Name: "ClickHouse", Status: "down", Detail: "unreachable"},
			componentHealth{Name: "Ingestion", Status: "unknown", Detail: "ClickHouse unreachable"},
		)
		writeJSON(w, http.StatusOK, resp)
		return nil
	}
	resp.Components = append(resp.Components, componentHealth{Name: "ClickHouse", Status: "healthy", Detail: "reachable"})

	stats, err := store.SystemStats(r.Context())
	if err != nil {
		resp.Overall = "degraded"
		resp.Components = append(resp.Components, componentHealth{Name: "Ingestion", Status: "unknown", Detail: "stats unavailable"})
		writeJSON(w, http.StatusOK, resp)
		return nil
	}

	retention := map[string]int{"traces": a.cfg.RetentionTracesDays, "logs": a.cfg.RetentionLogsDays, "metrics": a.cfg.RetentionMetricsDays, "profiles": a.cfg.RetentionProfilesDays}
	var newest *time.Time
	for _, sig := range stats.Signals {
		d := signalStatsDTO{
			Signal:          sig.Signal,
			Rows:            sig.Rows,
			Bytes:           sig.Bytes,
			CompressedBytes: sig.CompressedBytes,
			RetentionDays:   retention[sig.Signal],
		}
		if sig.CompressedBytes > 0 {
			d.Compression = float64(sig.Bytes) / float64(sig.CompressedBytes)
		}
		if sig.Oldest != nil {
			s := sig.Oldest.UTC().Format(time.RFC3339)
			d.Oldest = &s
		}
		if sig.Newest != nil {
			s := sig.Newest.UTC().Format(time.RFC3339)
			d.Newest = &s
			if newest == nil || sig.Newest.After(*newest) {
				newest = sig.Newest
			}
		}
		resp.Signals = append(resp.Signals, d)
	}
	for _, dk := range stats.Disks {
		resp.Disks = append(resp.Disks, diskDTO{Name: dk.Name, FreeBytes: dk.FreeBytes, TotalBytes: dk.TotalBytes})
	}

	ingestion := componentHealth{Name: "Ingestion"}
	switch {
	case newest == nil:
		ingestion.Status, ingestion.Detail = "idle", "no data yet"
	case now.Sub(*newest) <= ingestionFreshWindow:
		ingestion.Status, ingestion.Detail = "healthy", "last data "+humanizeAgo(now.Sub(*newest))
	default:
		ingestion.Status, ingestion.Detail = "idle", "last data "+humanizeAgo(now.Sub(*newest))
	}
	resp.Components = append(resp.Components, ingestion)

	resp.Overall = "healthy"
	if ingestion.Status != "healthy" {
		resp.Overall = "degraded"
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// humanizeAgo renders a coarse "Ns/Nm/Nh/Nd ago" for the ingestion detail line.
func humanizeAgo(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	}
}
