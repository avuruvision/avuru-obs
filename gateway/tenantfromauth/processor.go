package tenantfromauth

import (
	"context"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// tenantAttribute is the resource attribute the whole platform partitions on
// (the X-Avuru-Tenant / project slug). In enforce mode the validated key's
// project overwrites whatever the client claimed here.
const tenantAttribute = "avuru.tenant"

// authProjectAttribute is the client-auth attribute the avuruingestauth
// extension sets to the validated project. Kept as a plain string (not an
// import of the extension) so this processor's module stays independent.
const authProjectAttribute = "project"

// proc stamps avuru.tenant from the validated ingest-key project.
type proc struct{}

func newProc() *proc { return &proc{} }

// projectFromContext returns the validated project the auth extension attached,
// or "" when absent (auth off, log mode, or no key) — in which case every
// process* method is a pass-through and the existing resource/tenant +
// transform/tenant processors remain the tenant source, byte-identical to today.
func projectFromContext(ctx context.Context) string {
	info := client.FromContext(ctx)
	if info.Auth == nil {
		return ""
	}
	if s, ok := info.Auth.GetAttribute(authProjectAttribute).(string); ok {
		return s
	}
	return ""
}

func (p *proc) processTraces(ctx context.Context, td ptrace.Traces) (ptrace.Traces, error) {
	project := projectFromContext(ctx)
	if project == "" {
		return td, nil
	}
	rss := td.ResourceSpans()
	for i := 0; i < rss.Len(); i++ {
		rss.At(i).Resource().Attributes().PutStr(tenantAttribute, project)
	}
	return td, nil
}

func (p *proc) processMetrics(ctx context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	project := projectFromContext(ctx)
	if project == "" {
		return md, nil
	}
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rms.At(i).Resource().Attributes().PutStr(tenantAttribute, project)
	}
	return md, nil
}

func (p *proc) processLogs(ctx context.Context, ld plog.Logs) (plog.Logs, error) {
	project := projectFromContext(ctx)
	if project == "" {
		return ld, nil
	}
	rls := ld.ResourceLogs()
	for i := 0; i < rls.Len(); i++ {
		rls.At(i).Resource().Attributes().PutStr(tenantAttribute, project)
	}
	return ld, nil
}
