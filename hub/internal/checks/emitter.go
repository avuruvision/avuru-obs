package checks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

// OTLPEmitter publishes a span for each probe, over OTLP/HTTP to the gateway.
//
// This is the design's hinge, and the reason it is built this way. A check is
// not a side channel — it is synthetic traffic, so it belongs in RED, on the
// service map and in the trace explorer like any other client. That makes the
// failing-check-to-failing-trace click-through fall out of the existing product
// instead of needing a parallel health system.
//
// It matters HOW: the hub exports as an OTLP CLIENT of the gateway, exactly as
// an instrumented application would. It never writes otel_traces itself, even
// though it holds a ClickHouse connection and could. A direct insert would look
// simpler and would quietly bypass ingest-key enforcement, per-project routing
// and every transformation the pipeline applies — producing rows no sender
// could have produced. `agent_docs/architecture.md` locks "the hub is never in
// the telemetry byte-path"; going through the front door is what keeps that
// true (see design/2026-07-20-endpoint-checks.md).
//
// Encoded with the collector's own pdata, which the hub already depends on for
// the profiles ingest — so this costs no new dependency and no SDK.
type OTLPEmitter struct {
	endpoint string // full URL, e.g. http://gateway:4318/v1/traces
	key      string // ingest key, when auth.ingest is enforcing
	client   *http.Client
	service  string
	log      *slog.Logger
}

// NewOTLPEmitter builds an emitter for a gateway base endpoint
// (http://host:4318). Returns nil when base is empty: no gateway configured
// means checks still run and still record — the results are the point, the span
// is the correlation. A nil Emitter is handled by the scheduler.
func NewOTLPEmitter(base, ingestKey, serviceName string, log *slog.Logger) *OTLPEmitter {
	if base == "" {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		log.Warn("check span emission disabled: unparseable gateway endpoint", "endpoint", base)
		return nil
	}
	u.Path = "/v1/traces"
	if serviceName == "" {
		serviceName = "avuru-obs-checks"
	}
	return &OTLPEmitter{
		endpoint: u.String(),
		key:      ingestKey,
		service:  serviceName,
		log:      log,
		// Short: a probe's span must never outlive the probe's own budget, and
		// a slow gateway must not become a slow health board.
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// EmitCheckSpan sends one Client span describing the probe and returns its
// trace/span ids so the stored result can point at it.
//
// Best-effort by design: a gateway that is down must not stop a check from
// recording that the endpoint it watches is down — that would be the failure
// mode taking out the alarm.
func (e *OTLPEmitter) EmitCheckSpan(ctx context.Context, r Result, target string) (string, string) {
	traceID, spanID := newIDs()

	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	res := rs.Resource().Attributes()
	res.PutStr("service.name", e.service)
	// Marks this as the platform's own synthetic traffic, so the existing
	// aux-span classification can keep it out of user-facing RED when asked.
	res.PutStr("avuru.synthetic", "check")
	if r.Tenant != "" {
		res.PutStr("avuru.tenant", r.Tenant)
	}

	ss := rs.ScopeSpans().AppendEmpty()
	ss.Scope().SetName("avuru-obs/checks")
	span := ss.Spans().AppendEmpty()
	span.SetTraceID(traceID)
	span.SetSpanID(spanID)
	span.SetName("check " + r.CheckID)
	span.SetKind(ptrace.SpanKindClient)
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(r.At))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(r.At.Add(r.Latency)))

	at := span.Attributes()
	at.PutStr("url.full", target)
	at.PutStr("avuru.check.id", r.CheckID)
	at.PutStr("avuru.check.group", r.Group)
	if r.Status > 0 {
		at.PutInt("http.response.status_code", int64(r.Status))
	}
	if r.OK {
		span.Status().SetCode(ptrace.StatusCodeOk)
	} else {
		span.Status().SetCode(ptrace.StatusCodeError)
		span.Status().SetMessage(r.Error)
	}

	body, err := ptraceotlp.NewExportRequestFromTraces(td).MarshalProto()
	if err != nil {
		e.log.Warn("check span not encoded", "check", r.CheckID, "error", err)
		return "", ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", ""
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if e.key != "" {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.log.Debug("check span not delivered", "check", r.CheckID, "error", err)
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		e.log.Debug("gateway refused a check span", "check", r.CheckID, "status", resp.StatusCode)
		return "", ""
	}
	return hex.EncodeToString(traceID[:]), hex.EncodeToString(spanID[:])
}

func newIDs() (pcommon.TraceID, pcommon.SpanID) {
	var t pcommon.TraceID
	var s pcommon.SpanID
	// crypto/rand.Read never returns an error on any supported platform; a
	// zeroed id would be rejected downstream, so there is nothing to recover to.
	_, _ = rand.Read(t[:])
	_, _ = rand.Read(s[:])
	return t, s
}
