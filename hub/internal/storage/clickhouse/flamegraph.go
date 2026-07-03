package clickhouse

import (
	"context"
	"fmt"
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListProfiledServices returns services with profiling samples in the range,
// busiest first.
func (s *Store) ListProfiledServices(ctx context.Context, q storage.ProfileQuery) ([]storage.ProfiledService, error) {
	const query = `
SELECT ServiceName, sum(Value) AS total
FROM profiling_samples
WHERE Tenant = ? AND Timestamp >= ? AND Timestamp < ?
GROUP BY ServiceName
ORDER BY total DESC`
	rows, err := s.conn.Query(ctx, query, q.Tenant, q.Range.Start, q.Range.End)
	if err != nil {
		return nil, fmt.Errorf("profiled services: %w", err)
	}
	defer rows.Close()

	var out []storage.ProfiledService
	for rows.Next() {
		var p storage.ProfiledService
		if err := rows.Scan(&p.Name, &p.Samples); err != nil {
			return nil, fmt.Errorf("scanning profiled service: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProfileFlamegraph aggregates one service's samples into a flame graph:
// sum(Value) per stack in ClickHouse, folded into a root-first tree in Go.
func (s *Store) ProfileFlamegraph(ctx context.Context, q storage.ProfileQuery) (storage.FlameNode, error) {
	const query = `
SELECT any(st.Frames) AS frames, sum(sm.Value) AS total
FROM profiling_samples AS sm
INNER JOIN profiling_stacks AS st
    ON sm.StackHash = st.StackHash AND sm.Tenant = st.Tenant
WHERE sm.Tenant = ? AND sm.ServiceName = ?
  AND sm.Timestamp >= ? AND sm.Timestamp < ?
GROUP BY sm.StackHash`
	rows, err := s.conn.Query(ctx, query, q.Tenant, q.Service, q.Range.Start, q.Range.End)
	if err != nil {
		return storage.FlameNode{}, fmt.Errorf("flamegraph stacks: %w", err)
	}
	defer rows.Close()

	root := storage.FlameNode{Name: "root"}
	index := map[*storage.FlameNode]map[string]*storage.FlameNode{}
	for rows.Next() {
		var (
			frames []string
			total  uint64
		)
		if err := rows.Scan(&frames, &total); err != nil {
			return storage.FlameNode{}, fmt.Errorf("scanning stack row: %w", err)
		}
		addStack(&root, index, frames, total)
	}
	if err := rows.Err(); err != nil {
		return storage.FlameNode{}, err
	}
	sortChildren(&root)
	return root, nil
}

// addStack folds one leaf-first stack into the tree (walked root-first), adding
// value inclusively along the path and to Self at the leaf.
func addStack(root *storage.FlameNode, index map[*storage.FlameNode]map[string]*storage.FlameNode, frames []string, value uint64) {
	root.Value += value
	node := root
	for i := len(frames) - 1; i >= 0; i-- {
		name := frames[i]
		kids := index[node]
		if kids == nil {
			kids = map[string]*storage.FlameNode{}
			index[node] = kids
		}
		child := kids[name]
		if child == nil {
			child = &storage.FlameNode{Name: name}
			kids[name] = child
			node.Children = append(node.Children, child)
		}
		child.Value += value
		node = child
	}
	node.Self += value
}

func sortChildren(n *storage.FlameNode) {
	sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].Value > n.Children[j].Value })
	for _, c := range n.Children {
		sortChildren(c)
	}
}
