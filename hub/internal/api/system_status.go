package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
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

// projectSignalDTO is one signal's footprint for the SELECTED project. Rows,
// the time bounds and the rate are counted with the tenant filter;
// EstimatedBytes is apportioned from the table's compressed size by row share,
// because parts hold every tenant's rows together. The field name says so, and
// the UI labels it.
type projectSignalDTO struct {
	Signal         string  `json:"signal"`
	Rows           uint64  `json:"rows"`
	EstimatedBytes uint64  `json:"estimatedBytes"`
	Oldest         *string `json:"oldest,omitempty"`
	Newest         *string `json:"newest,omitempty"`
	RowsPerMinute  float64 `json:"rowsPerMinute"`
	// RetentionDays is what THIS project keeps for this signal: its own window
	// when it has one, otherwise the install-wide number, with Inherited saying
	// which — so "7 days" is never ambiguous about where it came from.
	RetentionDays int  `json:"retentionDays"`
	Inherited     bool `json:"inherited"`
}

// projectUsageDTO is the per-project half of the Status page: what the selected
// project holds, as opposed to what the install holds. An aggregate reports the
// union of the members the caller may see — exactly the set its screens read —
// and names them, so a number that spans clusters never looks like one cluster's.
type projectUsageDTO struct {
	ID      string   `json:"id"`
	Tenants []string `json:"tenants"`
	// RetentionVaries is set when an aggregate's members do not all keep the
	// same window. There is no single honest number to print then, so the UI
	// points at the members instead of averaging them.
	RetentionVaries bool               `json:"retentionVaries,omitempty"`
	Signals         []projectSignalDTO `json:"signals"`
}

type systemStatusResponse struct {
	Version    string                `json:"version"`
	Overall    string                `json:"overall"` // healthy | degraded | down
	CheckedAt  string                `json:"checkedAt"`
	Components []componentHealth     `json:"components"`
	Signals    []signalStatsDTO      `json:"signals"`
	Disks      []diskDTO             `json:"disks"`
	Connection *storageConnectionDTO `json:"connection,omitempty"`
	// Project is what the SELECTED project holds. Absent when the per-project
	// read failed — the instance-wide half of the page must still render, which
	// is the same reason this endpoint answers 200 with ClickHouse down.
	Project *projectUsageDTO `json:"project,omitempty"`
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

	// Per-project usage. Computed after the instance-wide numbers and allowed
	// to fail on its own: the page's health verdict must not depend on one
	// project's counts.
	if pu, err := a.projectUsage(r, store, retention, now); err != nil {
		resp.Components = append(resp.Components,
			componentHealth{Name: "Project usage", Status: "unknown", Detail: "per-project stats unavailable"})
	} else {
		resp.Project = pu
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

// projectUsage answers "what does THIS project hold?" — the per-project half of
// the Status page. The tenant set comes from the same resolver the screens use,
// so an aggregate reports its members' union and a leaf reports itself.
func (a *API) projectUsage(r *http.Request, store storage.Store, globalRetention map[string]int, now time.Time) (*projectUsageDTO, error) {
	project, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	usage, err := store.TenantUsage(r.Context(), tenants, now)
	if err != nil {
		return nil, err
	}
	own, varies, err := a.projectRetention(r.Context(), project, tenants)
	if err != nil {
		return nil, err
	}
	out := &projectUsageDTO{ID: project, Tenants: tenants, RetentionVaries: varies}
	for _, u := range usage.Signals {
		d := projectSignalDTO{
			Signal:         u.Signal,
			Rows:           u.Rows,
			EstimatedBytes: u.EstimatedBytes,
			RowsPerMinute:  u.RowsPerMinute,
			RetentionDays:  globalRetention[u.Signal],
			Inherited:      true,
		}
		// A project window shorter than the install's is what actually applies
		// (the trimmer runs before the table TTL would). The API refuses a
		// longer one, so a stored window is always the effective one.
		if own > 0 {
			d.RetentionDays, d.Inherited = own, false
		}
		if u.Oldest != nil {
			v := u.Oldest.UTC().Format(time.RFC3339)
			d.Oldest = &v
		}
		if u.Newest != nil {
			v := u.Newest.UTC().Format(time.RFC3339)
			d.Newest = &v
		}
		out.Signals = append(out.Signals, d)
	}
	return out, nil
}

// projectRetention returns the window that applies to project — its own, or 0
// when it inherits — and whether an AGGREGATE's members disagree. An aggregate
// never carries a window itself (the API refuses one), so the honest answer for
// a mixed membership is "it varies", not an average nobody configured.
func (a *API) projectRetention(ctx context.Context, project string, tenants []string) (own int, varies bool, err error) {
	projects, err := a.dbProjectsCached(ctx)
	if err != nil {
		return 0, false, err
	}
	byID := make(map[string]int, len(projects))
	for _, p := range projects {
		byID[p.ID] = p.RetentionDays
	}
	if len(tenants) == 1 && tenants[0] == project {
		return byID[project], false, nil
	}
	first, seen := 0, false
	for _, t := range tenants {
		d := byID[t]
		if !seen {
			first, seen = d, true
			continue
		}
		if d != first {
			return 0, true, nil
		}
	}
	return first, false, nil
}
