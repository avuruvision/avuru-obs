package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

const defaultAgentWindow = 10 * time.Minute

// ListAgentNodes derives the sensor inventory from the data itself: a node is
// "reporting" when any signal carries its k8s.node.name inside the window.
// Container-level liveness is inferred, not observed — an explicit agent
// status channel arrives with OpAMP (v0.2).
func (s *Store) ListAgentNodes(ctx context.Context, q storage.AgentQuery) ([]storage.AgentNode, error) {
	window := q.Window
	if window <= 0 {
		window = defaultAgentWindow
	}
	since := time.Now().UTC().Add(-window)

	nodes := map[string]*storage.AgentNode{}
	get := func(name string) *storage.AgentNode {
		if n, ok := nodes[name]; ok {
			return n
		}
		n := &storage.AgentNode{Node: name}
		nodes[name] = n
		return n
	}

	// One (table, time column, target field) probe per signal. Metrics uses
	// the kubeletstats node gauges — the strongest agent-liveness signal.
	probes := []struct {
		query  string
		args   []any
		assign func(n *storage.AgentNode, t time.Time)
	}{
		{
			query: `SELECT ` + nodeAttr + ` AS node, max(TimeUnix) AS newest
FROM otel_metrics_gauge
WHERE Tenant = ? AND TimeUnix >= ? AND MetricName IN (?, ?) AND ` + nodeAttr + ` != ''
GROUP BY node`,
			args:   []any{q.Tenant, since, metricNodeCPU, metricNodeMem},
			assign: func(n *storage.AgentNode, t time.Time) { n.Metrics = &t },
		},
		{
			query: `SELECT ` + nodeAttr + ` AS node, max(Timestamp) AS newest
FROM otel_logs
WHERE Tenant = ? AND Timestamp >= ? AND ` + nodeAttr + ` != ''
GROUP BY node`,
			args:   []any{q.Tenant, since},
			assign: func(n *storage.AgentNode, t time.Time) { n.Logs = &t },
		},
		{
			query: `SELECT ` + nodeAttr + ` AS node, max(Timestamp) AS newest
FROM otel_traces
WHERE Tenant = ? AND Timestamp >= ? AND ` + nodeAttr + ` != ''
GROUP BY node`,
			args:   []any{q.Tenant, since},
			assign: func(n *storage.AgentNode, t time.Time) { n.Traces = &t },
		},
		{
			query: `SELECT Node AS node, max(Timestamp) AS newest
FROM profiling_samples
WHERE Tenant = ? AND Timestamp >= ? AND Node != ''
GROUP BY node`,
			args:   []any{q.Tenant, since},
			assign: func(n *storage.AgentNode, t time.Time) { n.Profiles = &t },
		},
	}

	for _, p := range probes {
		rows, err := s.conn.Query(ctx, p.query, p.args...)
		if err != nil {
			return nil, fmt.Errorf("agent inventory probe: %w", err)
		}
		for rows.Next() {
			var (
				name   string
				newest time.Time
			)
			if err := rows.Scan(&name, &newest); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scanning agent probe: %w", err)
			}
			n := get(name)
			p.assign(n, newest)
			if newest.After(n.LastSeen) {
				n.LastSeen = newest
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]storage.AgentNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out, nil
}
