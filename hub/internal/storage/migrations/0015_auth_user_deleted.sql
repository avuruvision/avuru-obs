-- Tombstone column for auth_user. 0010 deliberately omitted it ("users are
-- disabled, never hard-deleted"); AEP 2026-08-06-users-crud-password amends
-- that decision: delete is now allowed as an explicit second step after
-- disable. Reads filter Deleted = 0; DeleteAuthUser writes the tombstone.
ALTER TABLE otel.auth_user ADD COLUMN IF NOT EXISTS `Deleted` UInt8 DEFAULT 0;
