package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// virtualTargetLimit caps how many (service, target) pairs one map read can
// produce. It is a blast-radius guard, not a paging mechanism: the naming rule
// deliberately collapses ports and destinations, so a real cluster lands two
// orders of magnitude below this. A cap that bites means the naming rule let
// something high-cardinality through — a bug to fix, not a limit to raise.
const virtualTargetLimit = 200

// dbSystemExpr is the database system a span names, tolerating both semconv
// generations: db.system.name is current, db.system is what most SDKs in the
// field still emit. Preferring the newer key means a span carrying both (an SDK
// mid-migration) is counted once, under one name.
const dbSystemExpr = `if(SpanAttributes['db.system.name'] != '',
            SpanAttributes['db.system.name'],
            SpanAttributes['db.system'])`

// msgSystemExpr is the messaging system a span names.
const msgSystemExpr = `SpanAttributes['messaging.system']`

// peerExpr resolves the target's identity from the caller's span. Order matters:
// the network address is the thing being depended on, and the logical names
// (schema, database, topic) only stand in when no address was recorded. The
// PORT is deliberately absent — including it splits one database into a node per
// connection endpoint and tells the reader nothing.
const peerExpr = `multiIf(
            SpanAttributes['server.address']             != '', SpanAttributes['server.address'],
            SpanAttributes['net.peer.name']              != '', SpanAttributes['net.peer.name'],
            SpanAttributes['db.namespace']               != '', SpanAttributes['db.namespace'],
            SpanAttributes['db.name']                    != '', SpanAttributes['db.name'],
            SpanAttributes['messaging.destination.name'] != '', SpanAttributes['messaging.destination.name'],
            '')`

// cacheSystems are the systems drawn as a cache rather than a database. Anything
// else that names a db system is a database: a node labelled with a system we
// don't recognise is far more useful than no node at all.
const cacheSystems = `('redis', 'valkey', 'memcached', 'keydb')`

// VirtualTargets derives the infrastructure dependencies that never send
// telemetry of their own — databases, caches and message brokers — from the exit
// spans of the services that call them.
//
// The classification IS the filter: a span is admitted only when it names a
// system (db.system[.name] or messaging.system). That is what makes an anti-join
// against child spans unnecessary — a database emits no OTLP, so an exit span
// naming one cannot have an instrumented callee the map already draws. A
// database PROXY that emits spans and declares db.system would appear twice;
// that is a known, documented trade for not paying a self-join on every map read
// (see design/2026-08-23-virtual-targets.md).
//
// Consumer spans are read in the opposite direction so a broker is drawn with
// traffic coming out of it as well as going in.
func (s *Store) VirtualTargets(ctx context.Context, q storage.ServiceQuery) ([]storage.VirtualTarget, error) {
	// The inner SELECT resolves the per-span facts; the outer one groups them.
	// Two levels rather than one because WHERE cannot lean on the aliases the
	// projection defines, and repeating a multiIf four times in a filter is how
	// the two copies drift apart.
	inner := `
    SELECT
        ServiceName                             AS svc,
        ` + dbSystemExpr + `                    AS db,
        ` + msgSystemExpr + `                   AS msg,
        ` + peerExpr + `                        AS peer,
        if(SpanKind = 'Consumer', 'in', 'out')  AS direction,
        ` + errorSpanExpr("") + `               AS is_error,
        toFloat64(Duration)                     AS dur
    FROM otel_traces
    WHERE Tenant IN (?)
      AND Timestamp >= ? AND Timestamp < ?
      AND SpanKind IN ('Client', 'Producer', 'Consumer')`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End}
	if q.ExcludeAux {
		inner += auxExclusion("")
	}
	inner += `
      AND (` + dbSystemExpr + ` != '' OR ` + msgSystemExpr + ` != '')
      -- A Consumer span with no messaging system is ordinary background work
      -- (several SDKs use Consumer for it). It names no broker, so there is no
      -- target to draw and no direction to reverse.
      AND NOT (SpanKind = 'Consumer' AND ` + msgSystemExpr + ` = '')`

	query := `
SELECT
    svc,
    multiIf(msg != '', 'queue', db IN ` + cacheSystems + `, 'cache', 'database') AS kind,
    if(msg != '', msg, db)                    AS system,
    peer,
    direction,
    count()                                   AS calls,
    countIf(is_error)                         AS errors,
    quantiles(0.5, 0.95)(dur)                 AS qs
FROM (` + inner + `
)
GROUP BY svc, kind, system, peer, direction
ORDER BY calls DESC
LIMIT ` + fmt.Sprint(virtualTargetLimit)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("virtual targets: %w", err)
	}
	defer rows.Close()

	var out []storage.VirtualTarget
	for rows.Next() {
		var (
			v     storage.VirtualTarget
			quant []float64
		)
		if err := rows.Scan(&v.Service, &v.Kind, &v.System, &v.Peer, &v.Direction,
			&v.Count, &v.ErrorCount, &quant); err != nil {
			return nil, fmt.Errorf("scanning virtual target row: %w", err)
		}
		// Two quantiles asked for, three returned by nsQuantiles — the third is
		// zero, as on ServiceEdges.
		v.P50, v.P95, _ = nsQuantiles(quant)
		out = append(out, v)
	}
	return out, rows.Err()
}
