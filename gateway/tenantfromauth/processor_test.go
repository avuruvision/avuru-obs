package tenantfromauth

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// fakeAuthData is a client.AuthData exposing one project attribute.
type fakeAuthData struct{ project string }

func (d fakeAuthData) GetAttribute(name string) any {
	if name == authProjectAttribute {
		return d.project
	}
	return nil
}
func (d fakeAuthData) GetAttributeNames() []string { return []string{authProjectAttribute} }

func ctxWithProject(project string) context.Context {
	info := client.Info{}
	if project != "" {
		info.Auth = fakeAuthData{project: project}
	}
	return client.NewContext(context.Background(), info)
}

func tracesWithTenant(tenant string) ptrace.Traces {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	if tenant != "" {
		rs.Resource().Attributes().PutStr(tenantAttribute, tenant)
	}
	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	return td
}

func resourceTenant(td ptrace.Traces) string {
	v, ok := td.ResourceSpans().At(0).Resource().Attributes().Get(tenantAttribute)
	if !ok {
		return ""
	}
	return v.Str()
}

func TestStampsTenantFromAuth(t *testing.T) {
	p := newProc()
	ctx := ctxWithProject("payments")
	td := tracesWithTenant("attacker-claimed")

	out, err := p.processTraces(ctx, td)
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceTenant(out); got != "payments" {
		t.Fatalf("tenant = %q, want payments (key project must override client claim)", got)
	}
}

func TestPassthroughWhenNoAuth(t *testing.T) {
	p := newProc()
	// No auth data (log/off mode) → the client-supplied tenant is left untouched.
	td := tracesWithTenant("client-claimed")
	out, err := p.processTraces(ctxWithProject(""), td)
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceTenant(out); got != "client-claimed" {
		t.Fatalf("tenant = %q, want client-claimed (must pass through untouched)", got)
	}
}

func TestStampsMetricsAndLogs(t *testing.T) {
	p := newProc()
	ctx := ctxWithProject("billing")

	md := pmetric.NewMetrics()
	md.ResourceMetrics().AppendEmpty().Resource().Attributes().PutStr(tenantAttribute, "x")
	mout, err := p.processMetrics(ctx, md)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mout.ResourceMetrics().At(0).Resource().Attributes().Get(tenantAttribute); v.Str() != "billing" {
		t.Fatalf("metrics tenant = %q, want billing", v.Str())
	}

	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().Resource().Attributes().PutStr(tenantAttribute, "x")
	lout, err := p.processLogs(ctx, ld)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lout.ResourceLogs().At(0).Resource().Attributes().Get(tenantAttribute); v.Str() != "billing" {
		t.Fatalf("logs tenant = %q, want billing", v.Str())
	}
}
