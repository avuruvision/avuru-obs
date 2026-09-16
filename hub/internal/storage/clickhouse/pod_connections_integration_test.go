//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestPodConnectionsIntegration(t *testing.T) {
	s := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	insert := func(tenant, trace, id, parent, kind, ns, pod, peer, status string, ts time.Time) {
		t.Helper()
		err := s.conn.Exec(ctx, `INSERT INTO otel_traces
            (Tenant, Timestamp, TraceId, SpanId, ParentSpanId, SpanKind, ServiceName,
             ResourceAttributes, SpanAttributes, Duration, StatusCode)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, tenant, ts, trace, id, parent, kind, pod+"-service",
			map[string]string{"k8s.pod.name": pod, "k8s.namespace.name": ns, "k8s.node.name": "node-" + ns},
			map[string]string{"server.address": peer}, uint64(12*time.Millisecond), status)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, trace := range []string{"a", "b"} {
		insert("default", trace, "client", "", "Client", "shop", "web", "api", "Ok", base)
		insert("default", trace, "server", "client", "Server", "payments", "api", "", "Ok", base)
	}
	// Duplicate delivery must not double the request count.
	insert("default", "a", "client", "", "Client", "shop", "web", "api", "Ok", base)
	insert("default", "a", "server", "client", "Server", "payments", "api", "", "Ok", base)
	insert("default", "external", "client", "", "Client", "shop", "web", "api.example.test", "Error", base)
	// Same name in another namespace is a different Pod, never a self-edge.
	insert("default", "same-name", "client", "", "Client", "shop", "web", "web", "Ok", base)
	insert("default", "same-name", "server", "client", "Server", "other", "web", "", "Ok", base)
	// A matching ID in an ungranted tenant must neither leak nor join, even
	// when that tenant is subsequently part of a merged-project query.
	insert("default", "collision", "client", "", "Client", "shop", "web", "unknown", "Ok", base)
	insert("secret", "collision", "server", "client", "Server", "secret", "hidden", "", "Ok", base)
	insert("secret", "private", "client", "", "Client", "secret", "hidden", "private.example", "Ok", base)
	insert("default", "no-pod", "client", "", "Client", "shop", "", "no-pod.example", "Ok", base)
	insert("default", "no-peer", "client", "", "Client", "shop", "web", "", "Ok", base)
	insert("default", "expired", "client", "", "Client", "shop", "web", "expired.example", "Ok", base.Add(-time.Hour))
	q := storage.InfraQuery{Tenant: "default", Range: storage.TimeRange{Start: base.Add(-time.Second), End: base.Add(time.Second)}}
	got, err := s.PodConnections(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d edges: %+v", len(got), got)
	}
	if got[0].Calls != 2 || got[0].Target.Name != "api" || got[0].Target.Namespace != "payments" || got[0].P95 != 12*time.Millisecond {
		t.Fatalf("paired requests: %+v", got[0])
	}
	var external, sameName, collision bool
	for _, edge := range got {
		if edge.Peer == "api.example.test" {
			external = edge.Target.Name == "" && edge.Errors == 1
		}
		if edge.Target.Name == "web" {
			sameName = edge.Target.Namespace == "other"
		}
		if edge.Peer == "unknown" {
			collision = edge.Target.Name == ""
		}
	}
	if !external || !sameName || !collision {
		t.Fatalf("missing attribution guarantees: %+v", got)
	}
	q.Tenants = []string{"default", "secret"}
	got, err = s.PodConnections(ctx, q)
	if err != nil || len(got) != 5 {
		t.Fatalf("merged tenants: %v %+v", err, got)
	}
	for _, edge := range got {
		if edge.Source.Name == "web" && edge.Target.Name == "hidden" {
			t.Fatal("joined colliding IDs across tenants")
		}
	}
	q.Tenants = nil
	q.Node = "node-payments"
	got, err = s.PodConnections(ctx, q)
	if err != nil || len(got) != 1 || got[0].Calls != 2 {
		t.Fatalf("node filter must include incoming connections: %v %+v", err, got)
	}
	q.Node, q.Limit = "", 1
	got, err = s.PodConnections(ctx, q)
	if err != nil || len(got) != 1 {
		t.Fatalf("limit: %v %+v", err, got)
	}
}
