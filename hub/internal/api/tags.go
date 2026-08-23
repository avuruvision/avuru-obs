package api

import (
	"net/http"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/clickhouse"
)

type tagKeyDTO struct {
	// Key is the full attribute name (avuru.tag.team) — what a filter string
	// carries. Name is the same key without the reserved prefix, which is what
	// a person actually calls it.
	Key    string   `json:"key"`
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type tagsResponse struct {
	Tags []tagKeyDTO `json:"tags"`
}

// handleTags serves the business tags seen in the window, for filter
// discovery. Core, not module-gated: tags ride resource attributes on traces,
// which every install collects.
//
// Empty is the normal answer — nothing is mapped until an operator maps it.
func (a *API) handleTags(w http.ResponseWriter, r *http.Request) error {
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
	tags, err := store.TagKeys(r.Context(), storage.ServiceQuery{
		Tenant:  tenant,
		Tenants: tenants,
		Range:   tr,
	})
	if err != nil {
		return err
	}
	resp := tagsResponse{Tags: make([]tagKeyDTO, 0, len(tags))}
	for _, t := range tags {
		resp.Tags = append(resp.Tags, tagKeyDTO{
			Key:    t.Key,
			Name:   strings.TrimPrefix(t.Key, clickhouse.TagPrefix),
			Values: t.Values,
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
