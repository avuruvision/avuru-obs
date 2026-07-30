-- UI-managed runtime collection overlay (design/2026-07-27-collection-control-plane.md).
-- Exactly one logical row: Id is a constant so ReplacingMergeTree collapses
-- every write down to the single newest overlay — same mutable-state idiom
-- as alert_channel (0009), but keyed by a fixed Id instead of a natural list
-- key, since there is exactly one overlay per release, not a named list.
CREATE TABLE IF NOT EXISTS otel.collection_overlay
(
    `Id` LowCardinality(String) DEFAULT 'default',
    `Overlay` String,
    `UpdatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedBy` String
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Id);
