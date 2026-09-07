package api

import (
	"net/http"
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type meshResponseFlagDTO struct {
	Flag     string `json:"flag"`
	Requests uint64 `json:"requests"`
	// Meaning is the proxy's reason in words. Empty for a flag this product
	// does not know, which passes through verbatim rather than being dropped:
	// the proxy said it, and an operator can look it up.
	Meaning string `json:"meaning,omitempty"`
}

type meshDestinationVersionDTO struct {
	Version  string `json:"version"`
	Requests uint64 `json:"requests"`
}

type meshCallerOutcomeDTO struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Requests  uint64 `json:"requests"`
	Errors5xx uint64 `json:"errors5xx"`
}

// meshWorkloadRequestsResponse is one workload's requests as its proxy
// counted them, by the dimensions no application span carries.
//
// Measured, not available: whether the data plane is read at all is the
// security route's to say. This says only whether THIS workload had series.
type meshWorkloadRequestsResponse struct {
	Measured            bool                        `json:"measured"`
	Reason              string                      `json:"reason,omitempty"`
	Reporter            string                      `json:"reporter,omitempty"`
	ResponseFlags       []meshResponseFlagDTO       `json:"responseFlags"`
	DestinationVersions []meshDestinationVersionDTO `json:"destinationVersions"`
	Callers             []meshCallerOutcomeDTO      `json:"callers"`
	// UpstreamStatsHint is always present: the per-upstream counters a reader
	// would look for next are not collected by a default mesh, and the screen
	// should say so before anyone goes looking for a zero that means "not
	// looking".
	UpstreamStatsHint string `json:"upstreamStatsHint"`
}

// meshResponseFlagMeanings is the proxy's response-flag vocabulary, in
// words. The flags are Envoy's; the meanings are what an operator does about
// them. Unknown flags pass through with no meaning.
var meshResponseFlagMeanings = map[string]string{
	"-":   "none",
	"UO":  "upstream overflow — circuit breaker open",
	"URX": "retries exhausted",
	"UF":  "upstream connection failure",
	"UT":  "upstream request timeout",
	"NR":  "no route configured",
	"DC":  "downstream connection terminated",
	"RL":  "rate limited",
}

const meshUpstreamStatsHint = "per-upstream counters (pending overflow, outlier ejections) are not collected — a default mesh does not expose them; enable them on the proxy with meshConfig.defaultConfig.proxyStatsMatcher.inclusionPrefixes: [cluster.outbound] if you need them"

// handleMeshWorkloadRequests serves one workload's requests by response flag,
// destination version and caller.
func (a *API) handleMeshWorkloadRequests(w http.ResponseWriter, r *http.Request) error {
	namespace, name := r.PathValue("namespace"), r.PathValue("name")
	resp := meshWorkloadRequestsResponse{
		ResponseFlags:       []meshResponseFlagDTO{},
		DestinationVersions: []meshDestinationVersionDTO{},
		Callers:             []meshCallerOutcomeDTO{},
		UpstreamStatsHint:   meshUpstreamStatsHint,
	}
	if !a.modules.Enabled(modules.InfraMetrics) {
		resp.Reason = "data-plane metrics are stored by the infra-metrics module, which is not enabled on this install"
		writeJSON(w, http.StatusOK, resp)
		return nil
	}
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
	q := storage.ServiceQuery{Tenant: tenant, Tenants: tenants, Range: tr, MeshDataplaneJob: a.cfg.MeshDataplaneJob}
	b, err := store.MeshRequestBreakdown(r.Context(), q, namespace, name)
	if err != nil {
		return err
	}
	if !b.Measured {
		resp.Reason = "no data-plane series for " + namespace + "/" + name +
			" in this window — the security view says whether the proxies are being read at all"
		writeJSON(w, http.StatusOK, resp)
		return nil
	}
	resp.Measured, resp.Reporter = true, b.Reporter
	for flag, n := range b.ResponseFlags {
		resp.ResponseFlags = append(resp.ResponseFlags, meshResponseFlagDTO{
			Flag: flag, Requests: n, Meaning: meshResponseFlagMeanings[flag],
		})
	}
	sort.Slice(resp.ResponseFlags, func(i, j int) bool {
		a, b := resp.ResponseFlags[i], resp.ResponseFlags[j]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Flag < b.Flag
	})
	for version, n := range b.DestinationVersions {
		resp.DestinationVersions = append(resp.DestinationVersions, meshDestinationVersionDTO{Version: version, Requests: n})
	}
	sort.Slice(resp.DestinationVersions, func(i, j int) bool {
		a, b := resp.DestinationVersions[i], resp.DestinationVersions[j]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Version < b.Version
	})
	// Callers arrive sorted from storage, by requests then name.
	for _, c := range b.Callers {
		resp.Callers = append(resp.Callers, meshCallerOutcomeDTO{
			Namespace: c.Namespace, Name: c.Workload, Requests: c.Requests, Errors5xx: c.Errors5xx,
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
