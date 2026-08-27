package api

import (
	"net/http"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// breakdownDimensions is the public name of every grouping the API accepts.
// Parameterised dimensions are written `attribute:<key>` / `resource:<key>`,
// so one query parameter carries both halves and the closed set stays closed.
var breakdownDimensions = map[string]storage.BreakdownDimension{
	"service":   storage.BreakdownService,
	"operation": storage.BreakdownOperation,
	"kind":      storage.BreakdownKind,
	"status":    storage.BreakdownStatus,
}

// parseGroupBy splits the groupBy parameter into a dimension and its optional
// map key. An unknown name is rejected here rather than in SQL: the storage
// layer would also refuse it, but a 400 naming the accepted values is a far
// better answer than a 500 for what is always a caller mistake.
func parseGroupBy(raw string) (storage.BreakdownDimension, string, error) {
	if raw == "" {
		return storage.BreakdownService, "", nil
	}
	if name, key, ok := strings.Cut(raw, ":"); ok {
		switch name {
		case "attribute", "resource":
			if key == "" {
				return "", "", badRequest("groupBy %q needs a key after the colon", raw)
			}
			if name == "attribute" {
				return storage.BreakdownAttribute, key, nil
			}
			return storage.BreakdownResource, key, nil
		default:
			return "", "", badRequest("unknown groupBy %q: use attribute:<key> or resource:<key>", raw)
		}
	}
	dim, ok := breakdownDimensions[raw]
	if !ok {
		return "", "", badRequest("unknown groupBy %q: must be service, operation, kind, status, attribute:<key> or resource:<key>", raw)
	}
	return dim, "", nil
}

func parseScope(raw string) (storage.BreakdownScope, error) {
	switch raw {
	case "", "entry":
		return storage.ScopeEntry, nil
	case "root":
		return storage.ScopeRoot, nil
	case "all":
		return storage.ScopeAll, nil
	default:
		return "", badRequest("invalid scope %q: must be entry, root or all", raw)
	}
}

// handleTraceBreakdown groups spans by one dimension — the numbers behind the
// part-of-whole views. It takes the SAME filters as the trace search, so a
// breakdown and the trace list beneath it can never describe different traffic.
func (a *API) handleTraceBreakdown(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	groupBy, key, err := parseGroupBy(r.URL.Query().Get("groupBy"))
	if err != nil {
		return err
	}
	scope, err := parseScope(r.URL.Query().Get("scope"))
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 20)
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
	status := r.URL.Query().Get("status")
	if status != "" && status != "ok" && status != "error" && status != "refused" {
		return badRequest("invalid status: must be ok, refused or error")
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}

	bd, err := store.TraceBreakdown(r.Context(), storage.BreakdownQuery{
		Tenant:      tenant,
		Tenants:     tenants,
		Range:       tr,
		GroupBy:     groupBy,
		Key:         key,
		Scope:       scope,
		Service:     r.URL.Query().Get("service"),
		Operation:   r.URL.Query().Get("operation"),
		Status:      status,
		Tags:        parseTags(r),
		MinDuration: minDur,
		MaxDuration: maxDur,
		ExcludeAux:  !parseBool(r, "includeAux", false),
		Limit:       limit,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toBreakdownResponse(bd, r.URL.Query().Get("groupBy"), string(scope)))
	return nil
}
