package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// maxTagValues bounds how many distinct values are returned per key. The point
// of discovery is to fill a filter control, and a control listing a thousand
// values is a text box with extra steps — if a key has more values than this,
// it was mapped from something that is not an identity (the chart's own
// cardinality guidance says so) and truncating is the honest signal.
const maxTagValues = 20

// TagKeys returns the business tags seen on telemetry in the window, each with
// a bounded sample of its values, so the UI can offer them as filters instead
// of asking people to remember what the cluster is labelled with.
//
// Read from traces: they carry the resource attributes of every workload that
// took part in a request, which is the widest view of what is tagged. An
// install running the logs module without traces therefore discovers nothing
// here, even though its logs do carry tags and can be filtered by them —
// scanning a second table to close that gap is not worth doubling the cost of
// a type-ahead.
//
// The scan is bounded by the tenant + time partition, like every other
// discovery read.
func (s *Store) TagKeys(ctx context.Context, q storage.ServiceQuery) ([]storage.TagKey, error) {
	const query = `
SELECT
    tag.1                                AS key,
    groupUniqArray(?)(tag.2)             AS vals
FROM (
    SELECT arrayJoin(arrayFilter(
        p -> startsWith(p.1, ?),
        arrayZip(mapKeys(ResourceAttributes), mapValues(ResourceAttributes))
    )) AS tag
    FROM otel_traces
    WHERE Tenant IN (?)
      AND Timestamp >= ? AND Timestamp < ?
)
GROUP BY key
ORDER BY key`

	rows, err := s.conn.Query(ctx, query, maxTagValues, TagPrefix,
		tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End)
	if err != nil {
		return nil, fmt.Errorf("tag keys: %w", err)
	}
	defer rows.Close()

	var out []storage.TagKey
	for rows.Next() {
		var t storage.TagKey
		if err := rows.Scan(&t.Key, &t.Values); err != nil {
			return nil, fmt.Errorf("scanning tag row: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
