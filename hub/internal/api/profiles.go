package api

import (
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type profiledServiceDTO struct {
	Name    string `json:"name"`
	Samples uint64 `json:"samples"`
}

type profiledServicesResponse struct {
	Services []profiledServiceDTO `json:"services"`
}

type flameNodeDTO struct {
	Name     string         `json:"name"`
	Value    uint64         `json:"value"`
	Self     uint64         `json:"self,omitempty"`
	Children []flameNodeDTO `json:"children,omitempty"`
}

type flamegraphResponse struct {
	Root flameNodeDTO `json:"root"`
}

func toFlameDTO(n *storage.FlameNode) flameNodeDTO {
	dto := flameNodeDTO{Name: n.Name, Value: n.Value, Self: n.Self}
	if len(n.Children) > 0 {
		dto.Children = make([]flameNodeDTO, 0, len(n.Children))
		for _, c := range n.Children {
			dto.Children = append(dto.Children, toFlameDTO(c))
		}
	}
	return dto
}

// handleProfiledServices lists services with profiling data in the window.
func (a *API) handleProfiledServices(w http.ResponseWriter, r *http.Request) error {
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
	services, err := store.ListProfiledServices(r.Context(), storage.ProfileQuery{
		Tenant: tenant,
		Range:  tr,
	})
	if err != nil {
		return err
	}
	resp := profiledServicesResponse{Services: make([]profiledServiceDTO, 0, len(services))}
	for _, s := range services {
		resp.Services = append(resp.Services, profiledServiceDTO{Name: s.Name, Samples: s.Samples})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleFlamegraph serves one service's aggregated CPU flame graph.
func (a *API) handleFlamegraph(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	service := r.URL.Query().Get("service")
	if service == "" {
		return badRequest("service is required")
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	root, err := store.ProfileFlamegraph(r.Context(), storage.ProfileQuery{
		Tenant:  tenant,
		Range:   tr,
		Service: service,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, flamegraphResponse{Root: toFlameDTO(&root)})
	return nil
}
