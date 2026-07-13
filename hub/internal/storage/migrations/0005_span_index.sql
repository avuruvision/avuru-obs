-- Span-id lookup support: 0001 indexes TraceId but not SpanId, so
-- FindSpanTrace (the "paste a span id" search) would full-scan. Bloom filter
-- matches idx_trace_id's shape. ADD INDEX only applies to new parts; old
-- parts stay unindexed (scan, fine at current volume) — no MATERIALIZE.
ALTER TABLE otel.otel_traces ADD INDEX IF NOT EXISTS idx_span_id SpanId TYPE bloom_filter(0.001) GRANULARITY 1;
