package api

import (
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type zoneTrafficDTO struct {
	SrcZone string `json:"srcZone"`
	DstZone string `json:"dstZone"`
	Bytes   uint64 `json:"bytes"`
}

type zonesResponse struct {
	Zones []zoneTrafficDTO `json:"zones"`
}

// handleZoneTraffic serves cross-zone byte volume per zone pair.
//
// Deliberately not part of the service map: the map is a graph of workloads
// merged by (source, target), and a zone is node topology rather than a graph
// element. Folding zones into that response would tie two shapes with different
// lifecycles together for no gain.
//
// An empty list is a valid answer and the common one — accounting is opt-in at
// the sensor, and a single-zone cluster never produces a row.
func (a *API) handleZoneTraffic(w http.ResponseWriter, r *http.Request) error {
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
	zones, err := store.ZoneTraffic(r.Context(), storage.ServiceQuery{
		Tenant:  tenant,
		Tenants: tenants,
		Range:   tr,
	})
	if err != nil {
		return err
	}
	resp := zonesResponse{Zones: make([]zoneTrafficDTO, 0, len(zones))}
	for _, z := range zones {
		resp.Zones = append(resp.Zones, zoneTrafficDTO{
			SrcZone: z.SrcZone,
			DstZone: z.DstZone,
			Bytes:   z.Bytes,
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
