//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// exitSpan is a span with arbitrary SpanAttributes — VirtualTargets is driven
// entirely by them (db.system, messaging.system, server.address …), which the
// shared testSpan cannot express.
type exitSpan struct {
	service  string
	spanID   string
	kind     string
	name     string
	attrs    map[string]string
	duration time.Duration
	status   string
}

func insertExitSpans(t *testing.T, s *Store, ts time.Time, spans []exitSpan) {
	t.Helper()
	batch, err := s.conn.PrepareBatch(context.Background(), `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, SpanAttributes, Duration, StatusCode)`)
	if err != nil {
		t.Fatalf("preparing batch: %v", err)
	}
	for _, sp := range spans {
		status := sp.status
		if status == "" {
			status = "Unset"
		}
		d := sp.duration
		if d == 0 {
			d = time.Millisecond
		}
		if err := batch.Append(
			ts, "trace-"+sp.spanID, sp.spanID, "", sp.name, sp.kind, sp.service,
			map[string]string{"service.name": sp.service}, sp.attrs,
			uint64(d.Nanoseconds()), status,
		); err != nil {
			t.Fatalf("appending span: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending batch: %v", err)
	}
}

func targetKey(v storage.VirtualTarget) string {
	return v.Service + " " + v.Direction + " " + v.Kind + " " + v.System + "/" + v.Peer
}

// The whole feature in one fixture: a database, a cache, a broker written to and
// read from, and a plain HTTP exit that must NOT become a node.
func TestVirtualTargetsDerivation(t *testing.T) {
	s := startClickHouse(t)
	now := time.Now().UTC().Truncate(time.Second)
	tr := storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}

	insertExitSpans(t, s, now, []exitSpan{
		// Database, current semconv key, addressed by hostname.
		{service: "checkout", spanID: "v000000000000001", kind: "Client", name: "SELECT orders",
			attrs:    map[string]string{"db.system.name": "postgresql", "server.address": "orders-db"},
			duration: 40 * time.Millisecond},
		// Same target, prior semconv key — one node, not two.
		{service: "payments", spanID: "v000000000000002", kind: "Client", name: "SELECT orders",
			attrs: map[string]string{"db.system": "postgresql", "server.address": "orders-db"}},
		// Cache, classified by system name.
		{service: "checkout", spanID: "v000000000000003", kind: "Client", name: "GET session",
			attrs: map[string]string{"db.system": "redis", "net.peer.name": "session-cache"}},
		// Broker, both directions.
		{service: "orders", spanID: "v000000000000004", kind: "Producer", name: "orders.placed send",
			attrs: map[string]string{"messaging.system": "kafka", "server.address": "broker-0"}},
		{service: "shipping", spanID: "v000000000000005", kind: "Consumer", name: "orders.placed receive",
			attrs: map[string]string{"messaging.system": "kafka", "server.address": "broker-0"}},
		// A plain HTTP exit names no system: out of scope by design, because
		// admitting it would put every third-party API on the map.
		{service: "checkout", spanID: "v000000000000006", kind: "Client", name: "POST /charge",
			attrs: map[string]string{"server.address": "api.example.com"}},
		// A Consumer span with no messaging system is ordinary background work,
		// not a broker read.
		{service: "batch", spanID: "v000000000000007", kind: "Consumer", name: "process batch",
			attrs: map[string]string{}},
	})

	got, err := s.VirtualTargets(context.Background(), storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("VirtualTargets: %v", err)
	}

	found := map[string]storage.VirtualTarget{}
	for _, v := range got {
		found[targetKey(v)] = v
	}
	for _, want := range []string{
		"checkout out database postgresql/orders-db",
		"payments out database postgresql/orders-db",
		"checkout out cache redis/session-cache",
		"orders out queue kafka/broker-0",
		"shipping in queue kafka/broker-0",
	} {
		if _, ok := found[want]; !ok {
			t.Errorf("missing target %q; got %+v", want, got)
		}
	}
	if len(got) != 5 {
		t.Errorf("got %d targets, want 5 — the HTTP exit and the systemless consumer are not targets: %+v", len(got), got)
	}
	if v := found["checkout out database postgresql/orders-db"]; v.P95 < 39*time.Millisecond {
		t.Errorf("p95 = %v, want the caller's ~40ms client-span duration", v.P95)
	}
}

// Aux exclusion already drops driver keepalives (PING/SELECT 1). They must not
// invent a dependency either — a connection pool pinging Redis every second
// would otherwise be the busiest edge on the map.
func TestVirtualTargetsExcludeAuxPings(t *testing.T) {
	s := startClickHouse(t)
	now := time.Now().UTC().Truncate(time.Second)
	tr := storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}

	insertExitSpans(t, s, now, []exitSpan{
		{service: "poller", spanID: "v000000000000101", kind: "Client", name: "PING",
			attrs: map[string]string{"db.system": "redis", "server.address": "keepalive-cache"}},
		{service: "poller", spanID: "v000000000000102", kind: "Client", name: "GET user",
			attrs: map[string]string{"db.system": "redis", "server.address": "real-cache"}},
	})

	got, err := s.VirtualTargets(context.Background(), storage.ServiceQuery{
		Tenant: "default", Range: tr, ExcludeAux: true,
	})
	if err != nil {
		t.Fatalf("VirtualTargets: %v", err)
	}
	if len(got) != 1 || got[0].Peer != "real-cache" {
		t.Fatalf("got %+v, want only the real cache", got)
	}
}

// Peer resolution order, and the degradation when nothing names the target.
func TestVirtualTargetsPeerFallbacks(t *testing.T) {
	s := startClickHouse(t)
	now := time.Now().UTC().Truncate(time.Second)
	tr := storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}

	insertExitSpans(t, s, now, []exitSpan{
		// Address wins over the logical schema name.
		{service: "a", spanID: "v000000000000201", kind: "Client", name: "SELECT 2",
			attrs: map[string]string{"db.system": "mysql", "server.address": "db-host", "db.namespace": "shop"}},
		// No address: the schema stands in.
		{service: "b", spanID: "v000000000000202", kind: "Client", name: "SELECT 2",
			attrs: map[string]string{"db.system": "mysql", "db.namespace": "shop"}},
		// Neither: the target degrades to the bare system, which is still true.
		{service: "c", spanID: "v000000000000203", kind: "Client", name: "SELECT 2",
			attrs: map[string]string{"db.system": "mysql"}},
		// Messaging with no address falls back to the destination.
		{service: "d", spanID: "v000000000000204", kind: "Producer", name: "send",
			attrs: map[string]string{"messaging.system": "rabbitmq", "messaging.destination.name": "jobs"}},
	})

	got, err := s.VirtualTargets(context.Background(), storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("VirtualTargets: %v", err)
	}
	peers := map[string]string{}
	for _, v := range got {
		peers[v.Service] = v.Peer
	}
	for svc, want := range map[string]string{"a": "db-host", "b": "shop", "c": "", "d": "jobs"} {
		if peers[svc] != want {
			t.Errorf("peer(%s) = %q, want %q", svc, peers[svc], want)
		}
	}
}
