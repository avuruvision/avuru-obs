-- UI-authored rate table (design/2026-08-30-agents-budgets-and-rates.md).
-- Exactly one logical row: Id is a constant so ReplacingMergeTree collapses
-- every write down to the single newest overlay — the same mutable-state idiom
-- as collection_overlay (0014), and for the same reason: there is one rate
-- table per install, not a named list.
--
-- Rates span the cost and ai modules both, so neither owns this table; it is
-- created on every install like collection_overlay, and only the API routes
-- and the surfaces that read it are gated.
CREATE TABLE IF NOT EXISTS {db}.rates_overlay
(
    `Id` LowCardinality(String) DEFAULT 'default',
    `Overlay` String,
    `UpdatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedBy` String
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Id);
