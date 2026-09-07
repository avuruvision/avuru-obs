package clickhouse

// seriesIDExpr isolates one exported counter series: metric name plus the
// full attribute AND resource-attribute identity. Every cumulative-counter
// read that wants a window's contribution computes max−min per series FIRST
// and sums after, so multi-zone / multi-container / multi-metric series can
// never cross-cancel each other's delta. mapSort: a Map column preserves
// insertion order and writers don't guarantee a stable pair order, so hashing
// must be order-insensitive or one series splits into many (every delta
// collapsing to 0).
//
// One expression, shared: the green module wrote it first, the mesh reads use
// it now, and a second copy would be a second place for the identity to drift.
const seriesIDExpr = "cityHash64(MetricName, toString(mapSort(Attributes)), toString(mapSort(ResourceAttributes)))"

// seriesDeltaExpr is a series' contribution over the window: the counter's
// rise between its lowest and highest sample. greatest(…, 0) is purely
// defensive — max ≥ min always holds inside a group, so it can never fire; a
// guard against future reshapes of the expression. A counter reset WITHIN the
// window overcounts by up to the pre-reset value (max stays the pre-reset
// high-water mark while min is the post-reset low); that is the documented
// cumulative-counter approximation, and it is a far smaller lie than
// sum(Value), which reports a cumulative counter scraped ten times as ten
// times its value.
const seriesDeltaExpr = "greatest(max(Value) - min(Value), 0)"
