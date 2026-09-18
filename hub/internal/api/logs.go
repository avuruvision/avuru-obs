package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type logRecordDTO struct {
	Timestamp  time.Time         `json:"timestamp"`
	Severity   string            `json:"severity"`
	Service    string            `json:"service"`
	Source     string            `json:"source"`
	Body       string            `json:"body"`
	TraceID    string            `json:"traceId,omitempty"`
	SpanID     string            `json:"spanId,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type logsResponse struct {
	Logs       []logRecordDTO `json:"logs"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

func toLogRecordDTO(l storage.LogRecord) logRecordDTO {
	return logRecordDTO{
		Timestamp:  l.Timestamp.UTC(),
		Severity:   l.Severity,
		Service:    l.Service,
		Source:     displayLogSource(l),
		Body:       l.Body,
		TraceID:    l.TraceID,
		SpanID:     l.SpanID,
		Attributes: l.Attributes,
	}
}

func displayLogSource(l storage.LogRecord) string {
	if l.Source != "" {
		return l.Source
	}
	name := strings.ToLower(l.Service)
	switch {
	case name == "":
		return "other"
	case name == "ztunnel" || strings.HasPrefix(name, "ztunnel-"):
		return logSourceZtunnel
	case name == "waypoint" || strings.HasPrefix(name, "waypoint-") || strings.HasSuffix(name, "-waypoint"):
		return logSourceWaypoint
	default:
		return "application"
	}
}

type logResolutionDTO struct {
	Service            string `json:"service,omitempty"`
	Namespace          string `json:"namespace,omitempty"`
	Workload           string `json:"workload,omitempty"`
	ProxiesUnavailable string `json:"proxiesUnavailable,omitempty"`
	// ProxiesMatchedBy says how the proxy lines were tied to the subject when
	// the precise pod set was not available: the descriptor's fallback rung
	// (mesh-config off, cluster unread, pod list cut, no pod in the snapshot).
	// Empty when pods were matched precisely.
	ProxiesMatchedBy string `json:"proxiesMatchedBy,omitempty"`
	// ProxiesFallback is set when pagination had to compact a large pod set
	// into the stable workload needles; distinct from ProxiesMatchedBy.
	ProxiesFallback string `json:"proxiesFallback,omitempty"`
	// SourceBranches is the exact resolved store query held behind the opaque
	// ResolutionToken. A non-nil empty slice means "match nothing".
	SourceBranches []logSourceBranchDTO `json:"-"`
}

type logSourceBranchDTO struct {
	Category string     `json:"category"`
	Services []string   `json:"services"`
	BodyAll  [][]string `json:"bodyAll,omitempty"`
}

type multiLogsResponse struct {
	logsResponse
	Resolutions     []logResolutionDTO `json:"resolutions"`
	ResolutionToken string             `json:"resolutionToken,omitempty"`
}

func (a *API) handleSearchLogs(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 50)
	if err != nil {
		return err
	}
	cursor, err := parseLogCursor(r)
	if err != nil {
		return err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	services := uniqueQueryValues(q["service"])
	workloads := uniqueQueryValues(q["workload"])
	wanted, err := parseLogSources(q["source"]...)
	if err != nil {
		return err
	}
	hasSourceFilter := strings.TrimSpace(strings.Join(q["source"], ",")) != ""
	hasComposedSelection := (len(services) > 0 || len(workloads) > 0) &&
		(len(workloads) > 0 || len(services) > 1 || hasSourceFilter)
	logQuery := storage.LogQuery{
		Tenant:      tenant,
		Tenants:     tenants,
		Range:       tr,
		Tags:        parseTags(r),
		MinSeverity: q.Get("severity"),
		Query:       q.Get("q"),
		Limit:       limit,
		Cursor:      cursor,
	}
	resolutions := make([]logResolutionDTO, 0, len(services)+len(workloads))
	resolutionToken := strings.TrimSpace(q.Get("resolution"))
	if len(q["resolution"]) > 1 {
		return badRequest("invalid log resolution token")
	}
	if cursor != nil && resolutionToken == "" && hasComposedSelection {
		return badRequest("resolution token is required for multiservice pagination")
	}
	resolutionScope := makeLogResolutionScope(tenant, tenants, services, workloads, wanted, tr.Start, tr.End)
	resolved := map[string]logResolutionDTO{}
	if resolutionToken != "" {
		cached, decodeErr := decodeLogResolutionToken(a.logResolutionKey, resolutionToken, resolutionScope, time.Now())
		if decodeErr != nil {
			return badRequest("%s", decodeErr.Error())
		}
		for _, hint := range cached {
			resolved[resolutionKey(hint.Service, hint.Namespace, hint.Workload)] = hint
		}
	}
	// Keep the original single-service contract for old clients. The new
	// explorer always sends source= and therefore enters the composed path.
	if len(services) == 1 && len(workloads) == 0 && !hasSourceFilter {
		logQuery.Service = services[0]
	} else if len(services) > 0 || len(workloads) > 0 {
		sq := storage.ServiceQuery{Tenant: tenant, Tenants: tenants, Range: tr}
		for _, service := range services {
			if hint, ok := resolved[resolutionKey(service, "", "")]; ok {
				logQuery.Sources = append(logQuery.Sources, storageSources(hint.SourceBranches)...)
				resolutions = append(resolutions, hint)
				continue
			}
			namespace, workload, why := "", "", ""
			namespace, workload, why = a.resolveServiceWorkload(r, store, sq, service)
			var desc workloadLogSources
			if workload == "" {
				desc = workloadLogSources{meshLogSourcesDTO: meshLogSourcesDTO{App: []string{service}, ProxiesUnavailable: why}}
			} else {
				desc = a.workloadLogSources(r, namespace, workload, "", service)
			}
			branches := desc.sources(wanted)
			logQuery.Sources = append(logQuery.Sources, branches...)
			resolutions = append(resolutions, logResolutionDTO{Service: service, Namespace: namespace, Workload: workload, ProxiesUnavailable: why, ProxiesMatchedBy: desc.Fallback, SourceBranches: sourceBranchDTOs(branches)})
		}
		for _, selected := range workloads {
			namespace, workload, ok := strings.Cut(selected, "/")
			if !ok || namespace == "" || workload == "" {
				return badRequest("workload must be namespace/name")
			}
			if hint, found := resolved[resolutionKey("", namespace, workload)]; found {
				logQuery.Sources = append(logQuery.Sources, storageSources(hint.SourceBranches)...)
				resolutions = append(resolutions, hint)
				continue
			}
			desc := a.workloadLogSources(r, namespace, workload, "")
			branches := desc.sources(wanted)
			logQuery.Sources = append(logQuery.Sources, branches...)
			resolutions = append(resolutions, logResolutionDTO{Namespace: namespace, Workload: workload, ProxiesMatchedBy: desc.Fallback, SourceBranches: sourceBranchDTOs(branches)})
		}
		logQuery.MatchNone = len(logQuery.Sources) == 0
	} else if hasSourceFilter {
		logQuery.SourceCategories = wantedSourceCategories(wanted)
	}
	if hasComposedSelection && resolutionToken == "" {
		resolutionToken, resolutions, err = encodeLogResolutionToken(a.logResolutionKey, resolutionScope, resolutions, time.Now())
		if err != nil {
			return badRequest("%s", err.Error())
		}
		logQuery.Sources = logSourcesFromResolutions(resolutions)
		logQuery.MatchNone = len(logQuery.Sources) == 0
	}
	page, err := store.SearchLogs(r.Context(), logQuery)
	if err != nil {
		return err
	}
	resp := multiLogsResponse{
		logsResponse: logsResponse{Logs: make([]logRecordDTO, 0, len(page.Logs)), NextCursor: encodeLogCursor(page.NextCursor)},
		Resolutions:  resolutions,
	}
	if page.NextCursor != nil {
		resp.ResolutionToken = resolutionToken
	}
	for _, l := range page.Logs {
		resp.Logs = append(resp.Logs, toLogRecordDTO(l))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func resolutionKey(service, namespace, workload string) string {
	if service = strings.TrimSpace(service); service != "" {
		return "service:" + service
	}
	if namespace != "" && workload != "" {
		return "workload:" + namespace + "/" + workload
	}
	return ""
}

func sourceBranchDTOs(sources []storage.LogSource) []logSourceBranchDTO {
	out := make([]logSourceBranchDTO, 0, len(sources))
	for _, source := range sources {
		out = append(out, logSourceBranchDTO{Category: source.Category, Services: source.Services, BodyAll: source.BodyAll})
	}
	return out
}

func storageSources(sources []logSourceBranchDTO) []storage.LogSource {
	out := make([]storage.LogSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, storage.LogSource{Category: source.Category, Services: source.Services, BodyAll: source.BodyAll})
	}
	return out
}

func logSourcesFromResolutions(resolutions []logResolutionDTO) []storage.LogSource {
	var out []storage.LogSource
	for _, resolution := range resolutions {
		out = append(out, storageSources(resolution.SourceBranches)...)
	}
	return out
}

func uniqueQueryValues(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func wantedSourceCategories(wanted map[string]bool) []string {
	out := make([]string, 0, 4)
	for _, source := range []string{logSourceApp, logSourceZtunnel, logSourceWaypoint, logSourceOther} {
		if !wanted[source] {
			continue
		}
		if source == logSourceApp {
			out = append(out, "application")
		} else {
			out = append(out, source)
		}
	}
	return out
}

func (a *API) handleLogsForTrace(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	traceID := r.PathValue("traceId")
	if traceID == "" {
		return badRequest("missing traceId")
	}
	_, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	logs, err := store.LogsForTrace(r.Context(), tenants, traceID)
	if err != nil {
		return err
	}
	resp := logsResponse{Logs: make([]logRecordDTO, 0, len(logs))}
	for _, l := range logs {
		resp.Logs = append(resp.Logs, toLogRecordDTO(l))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// Log cursor wire format: base64("<unix nanos>,<service>,<traceId>,<spanId>").
// The former three-field shape remains accepted during rolling upgrades.
func encodeLogCursor(c *storage.LogCursor) string {
	if c == nil {
		return ""
	}
	if c.Legacy {
		raw := fmt.Sprintf("%d,%s,%s", c.Timestamp.UnixNano(), c.TraceID, c.SpanID)
		return base64.RawURLEncoding.EncodeToString([]byte(raw))
	}
	raw := fmt.Sprintf("%d,%s,%s,%s", c.Timestamp.UnixNano(), c.Service, c.TraceID, c.SpanID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func parseLogCursor(r *http.Request) (*storage.LogCursor, error) {
	v := r.URL.Query().Get("cursor")
	if v == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, badRequest("invalid cursor")
	}
	parts := strings.SplitN(string(raw), ",", 4)
	if len(parts) != 3 && len(parts) != 4 {
		return nil, badRequest("invalid cursor")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, badRequest("invalid cursor")
	}
	if len(parts) == 3 {
		return &storage.LogCursor{Timestamp: time.Unix(0, ns).UTC(), TraceID: parts[1], SpanID: parts[2], Legacy: true}, nil
	}
	return &storage.LogCursor{Timestamp: time.Unix(0, ns).UTC(), Service: parts[1], TraceID: parts[2], SpanID: parts[3]}, nil
}
