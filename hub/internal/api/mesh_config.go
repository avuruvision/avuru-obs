package api

import (
	"net/http"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
)

// meshFindingDTO is one problem with one object.
//
// Message and Hint stay separate on the wire because they are separate jobs: a
// finding that only states the problem sends the reader looking, and naming the
// fix is the discipline this product applies everywhere else it reports a gap.
type meshFindingDTO struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
	// Ref names the object the finding is ABOUT when that differs from the one
	// it was found on — the Service a route cannot reach, say — so the reader
	// can search for the thing that is absent.
	Ref string `json:"ref,omitempty"`
}

type meshConfigObjectDTO struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	// Spec is sent only for a single-object request: a list of two hundred
	// objects with their specs inlined is a payload nobody reads.
	Spec     map[string]any   `json:"spec,omitempty"`
	Findings []meshFindingDTO `json:"findings,omitempty"`
}

type meshConfigResponse struct {
	// Same shape as the namespaces response, and for the same reason: an
	// unreadable cluster must not render as a cluster with no configuration.
	State        string                `json:"state"`
	Reason       string                `json:"reason,omitempty"`
	MissingKinds []string              `json:"missingKinds,omitempty"`
	Truncated    bool                  `json:"truncated,omitempty"`
	Objects      []meshConfigObjectDTO `json:"objects"`
}

// handleMeshConfig lists configuration objects, optionally narrowed, and
// returns one object whole when `name` is given.
func (a *API) handleMeshConfig(w http.ResponseWriter, r *http.Request) error {
	snap := a.meshConfig().Snapshot(r.Context())
	resp := meshConfigResponse{
		State:        string(snap.State),
		Reason:       snap.Reason,
		MissingKinds: snap.MissingKinds,
		Truncated:    snap.Truncated,
		Objects:      []meshConfigObjectDTO{},
	}
	if snap.State != meshconfig.StateOK {
		writeJSON(w, http.StatusOK, resp)
		return nil
	}

	var (
		wantNS   = r.URL.Query().Get("namespace")
		wantKind = r.URL.Query().Get("kind")
		wantName = r.URL.Query().Get("name")
		// A single object is requested by all three together; only then is the
		// spec worth sending.
		single = wantName != ""
	)
	for _, o := range snap.Objects {
		if wantNS != "" && o.Namespace != wantNS {
			continue
		}
		if wantKind != "" && !strings.EqualFold(o.Kind, wantKind) {
			continue
		}
		if wantName != "" && o.Name != wantName {
			continue
		}
		dto := meshConfigObjectDTO{
			Kind:      o.Kind,
			Namespace: o.Namespace,
			Name:      o.Name,
			Findings:  toFindingDTOs(o.Findings),
		}
		if single {
			dto.Spec = o.Spec
		}
		resp.Objects = append(resp.Objects, dto)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func toFindingDTOs(findings []meshconfig.Finding) []meshFindingDTO {
	if len(findings) == 0 {
		return nil
	}
	out := make([]meshFindingDTO, 0, len(findings))
	for _, f := range findings {
		out = append(out, meshFindingDTO{
			Code:     string(f.Code),
			Severity: string(f.Severity),
			Message:  f.Message,
			Hint:     f.Hint,
			Ref:      f.Ref,
		})
	}
	return out
}
