package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Wire DTOs for the service-health module. Statuses reuse the hub's health
// vocabulary (healthy|degraded|down|idle); rates/latency are already derived
// numbers from the health package.

type healthWindowDTO struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type healthDependencyDTO struct {
	Service  string `json:"service"`
	Tier     string `json:"tier"`
	Critical bool   `json:"critical"`
	Status   string `json:"status"`
}

type healthMemberDTO struct {
	Service         string                `json:"service"`
	Tier            string                `json:"tier"`
	BaseStatus      string                `json:"baseStatus"`
	EffectiveStatus string                `json:"effectiveStatus"`
	Reason          string                `json:"reason"`
	SpanCount       uint64                `json:"spanCount"`
	RatePerSec      float64               `json:"ratePerSec"`
	ErrorRate       float64               `json:"errorRate"`
	P95Ms           float64               `json:"p95Ms"`
	Dependencies    []healthDependencyDTO `json:"dependencies,omitempty"`
}

type healthGroupDTO struct {
	Name        string            `json:"name"`
	Environment string            `json:"environment,omitempty"`
	Tier        string            `json:"tier"`
	Source      string            `json:"source"`
	TierSource  string            `json:"tierSource"`
	Status      string            `json:"status"`
	Reason      string            `json:"reason"`
	Counts      map[string]int    `json:"counts"`
	SpanCount   uint64            `json:"spanCount"`
	RatePerSec  float64           `json:"ratePerSec"`
	ErrorRate   float64           `json:"errorRate"`
	P95Ms       float64           `json:"p95Ms"`
	Members     []healthMemberDTO `json:"members"`
}

type healthGroupsResponse struct {
	Overall   string           `json:"overall"`
	CheckedAt string           `json:"checkedAt"`
	Window    healthWindowDTO  `json:"window"`
	Groups    []healthGroupDTO `json:"groups"`
	// Warnings carry declarations the hub could not honour (e.g. an invalid
	// avuru.tier). Informational: the board still renders.
	Warnings []string `json:"warnings,omitempty"`
}

// handleHealthGroups rolls per-service RED health up into consolidated group
// health for the tenant over the requested window.
func (a *API) handleHealthGroups(w http.ResponseWriter, r *http.Request) error {
	report, tr, err := a.buildHealthReport(r)
	if err != nil {
		return err
	}
	resp := healthGroupsResponse{
		Overall:   report.Overall,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Window:    healthWindowDTO{Start: tr.Start.UTC().Format(time.RFC3339), End: tr.End.UTC().Format(time.RFC3339)},
		Groups:    make([]healthGroupDTO, 0, len(report.Groups)),
		Warnings:  report.Warnings,
	}
	for _, g := range report.Groups {
		resp.Groups = append(resp.Groups, toHealthGroupDTO(g))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleHealthGroup drills into a single group by name (404 if absent). With
// environments, one name can address several groups; ?environment= narrows to
// one. Without it the first match wins, so existing links keep working.
func (a *API) handleHealthGroup(w http.ResponseWriter, r *http.Request) error {
	report, _, err := a.buildHealthReport(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	env := r.URL.Query().Get("environment")
	for _, g := range report.Groups {
		if g.Name != name {
			continue
		}
		if env != "" && g.Environment != env {
			continue
		}
		writeJSON(w, http.StatusOK, toHealthGroupDTO(g))
		return nil
	}
	return storage.ErrNotFound
}

// buildHealthReport gathers the RED population, grouping labels, and dependency
// edges for the request window and computes the health report. All three reads
// share one ServiceQuery so the service sets line up.
func (a *API) buildHealthReport(r *http.Request) (health.Report, storage.TimeRange, error) {
	store, err := a.store()
	if err != nil {
		return health.Report{}, storage.TimeRange{}, err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return health.Report{}, storage.TimeRange{}, err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return health.Report{}, storage.TimeRange{}, err
	}
	q := storage.ServiceQuery{
		Tenant:     tenant,
		Tenants:    tenants,
		Range:      tr,
		ExcludeAux: !parseBool(r, "includeAux", false),
	}
	stats, err := store.ListServices(r.Context(), q)
	if err != nil {
		return health.Report{}, tr, err
	}
	labels, err := store.ServiceLabels(r.Context(), q)
	if err != nil {
		return health.Report{}, tr, err
	}
	edges, err := store.ServiceEdges(r.Context(), q)
	if err != nil {
		return health.Report{}, tr, err
	}
	report := health.Rollup(a.groupsConfig(r.Context()), tr.End.Sub(tr.Start), stats, labels, edges)
	return report, tr, nil
}

func toHealthGroupDTO(g health.GroupHealth) healthGroupDTO {
	members := make([]healthMemberDTO, 0, len(g.Members))
	for _, m := range g.Members {
		deps := make([]healthDependencyDTO, 0, len(m.Dependencies))
		for _, d := range m.Dependencies {
			deps = append(deps, healthDependencyDTO{
				Service:  d.Service,
				Tier:     string(d.Tier),
				Critical: d.Critical,
				Status:   d.Status,
			})
		}
		members = append(members, healthMemberDTO{
			Service:         m.Service,
			Tier:            string(m.Tier),
			BaseStatus:      m.BaseStatus,
			EffectiveStatus: m.EffectiveStatus,
			Reason:          m.Reason,
			SpanCount:       m.SpanCount,
			RatePerSec:      m.RatePerSec,
			ErrorRate:       m.ErrorRate,
			P95Ms:           m.P95Ms,
			Dependencies:    deps,
		})
	}
	return healthGroupDTO{
		Name:        g.Name,
		Environment: g.Environment,
		Tier:        string(g.Tier),
		Source:      g.Source,
		TierSource:  g.TierSource,
		Status:      g.Status,
		Reason:      g.Reason,
		Counts:      g.Counts,
		SpanCount:   g.SpanCount,
		RatePerSec:  g.RatePerSec,
		ErrorRate:   g.ErrorRate,
		P95Ms:       g.P95Ms,
		Members:     members,
	}
}
