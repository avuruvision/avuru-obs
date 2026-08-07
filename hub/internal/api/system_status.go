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
	// RetentionDays is what this install is configured to keep; TTLDays is
	// what ClickHouse is enforcing. They differ when a retention value was
	// changed after the tables were created and the migration has not
	// re-applied the TTL — the configured number is then a wish, and the
	// Storage view says so instead of repeating it. 0 = no day-based TTL found.
	RetentionDays int `json:"retentionDays"`
	TTLDays       int `json:"ttlDays"`
}

// storageConnectionDTO is where the telemetry actually lives. Read-only by
// nature, not by policy: ClickHouse IS the store, so it cannot hold its own
// connection string — changing it is a redeploy. The password is never part of
// this, and the whole endpoint is admin-only.
type storageConnectionDTO struct {
	Address  string `json:"address"`
	Database string `json:"database"`
	Username string `json:"username,omitempty"`
	Protocol string `json:"protocol"`
}

type diskDTO struct {
	Name       string `json:"name"`
	FreeBytes  uint64 `json:"freeBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

type systemStatusResponse struct {
	Version    string                `json:"version"`
	Overall    string                `json:"overall"` // healthy | degraded | down
	CheckedAt  string                `json:"checkedAt"`
	Components []componentHealth     `json:"components"`
	Signals    []signalStatsDTO      `json:"signals"`
	Disks      []diskDTO             `json:"disks"`
	Connection *storageConnectionDTO `json:"connection,omitempty"`
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
	// Reported even when ClickHouse is unreachable below — "which address did
	// we fail to reach?" is the first question an outage raises.
	if a.cfg.StorageConnection.Address != "" {
		c := a.cfg.StorageConnection
		resp.Connection = &storageConnectionDTO{
			Address: c.Address, Database: c.Database, Username: c.Username, Protocol: "native",
		}
	}

	store := a.provider()
	if store == nil || store.Ping(r.Context()) != nil {
		resp.Overall = "down"
		resp.Components = append(resp.Components,
			componentHealth{Name: "ClickHouse", Status: "down", Detail: "unreachable"},
			componentHealth{Name: "Schema", Status: "unknown", Detail: "ClickHouse unreachable"},
			componentHealth{Name: "Ingestion", Status: "unknown", Detail: "ClickHouse unreachable"},
		)
		writeJSON(w, http.StatusOK, resp)
		return nil
	}
	resp.Components = append(resp.Components, componentHealth{Name: "ClickHouse", Status: "healthy", Detail: "reachable"})

	// An unmigrated database is the one failure that makes everything else
	// meaningless, so it outranks ingestion in the verdict below.
	schemaReady := true
	if a.cfg.SchemaStatus != nil {
		st := a.cfg.SchemaStatus()
		schemaReady = st.Ready
		c := componentHealth{Name: "Schema", Status: "healthy",
			Detail: fmt.Sprintf("%d/%d migrations applied", len(st.Applied), len(st.Expected))}
		if !schemaReady {
			c.Status = "down"
			c.Detail = fmt.Sprintf("%d of %d migrations applied to %q — run `hub migrate`",
				len(st.Applied), len(st.Expected), st.Database)
		}
		resp.Components = append(resp.Components, c)
	}

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
			TTLDays:         sig.TTLDays,
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
	if !schemaReady {
		resp.Overall = "down"
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
