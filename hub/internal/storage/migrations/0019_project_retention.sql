-- Per-project retention (AEP 2026-07-27-projects-completion). RetentionDays is
-- the window this project keeps, in days; 0 means "inherit the install's global
-- retention" and is what every existing row gets. It cannot be a per-project
-- table TTL — the telemetry tables are shared, and a TTL expression is not
-- per-value — so the hub's retention trimmer enforces it with bounded
-- lightweight mutations scoped by Tenant. The global TTL stays the backstop,
-- which is why only SHORTER-than-global windows are accepted.
ALTER TABLE {db}.project ADD COLUMN IF NOT EXISTS `RetentionDays` UInt16 DEFAULT 0;
