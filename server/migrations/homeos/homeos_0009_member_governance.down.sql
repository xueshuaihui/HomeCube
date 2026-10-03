-- Migration: 0009_member_governance (down)
-- 反向于 up：先删 0009 建的表与索引，再删它加的两列。
-- ADD COLUMN 的逆操作是 DROP COLUMN（Postgres 支持）；0006 既有列与 CHECK 一律不动。
-- 逐条 IF EXISTS，可整支重放。

BEGIN;

DROP TABLE IF EXISTS homeos.homeos_audit_log;

DROP INDEX IF EXISTS homeos.idx_homeos_invitations_family_status;

ALTER TABLE homeos.homeos_invitations
    DROP COLUMN IF EXISTS revoked_by,
    DROP COLUMN IF EXISTS revoked_at;

ALTER TABLE homeos.homeos_families
    DROP COLUMN IF EXISTS pver;

COMMIT;
