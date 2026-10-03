-- Migration: 0007_home_projection (down)
-- Purpose: Roll back the home/dynamics/message read models created by 0007.
-- 反向于 up：先删后建的索引与表，逐条 DROP ... IF EXISTS，可整支重放。
-- 本支不动 0004 的 homeos_proj_finance 与 0006 的身份族表。

BEGIN;

DROP INDEX IF EXISTS homeos.homeos_notification_member_dedupe_uidx;
DROP INDEX IF EXISTS homeos.homeos_notification_member_created_idx;
DROP INDEX IF EXISTS homeos.homeos_notification_member_type_unread_idx;
DROP TABLE IF EXISTS homeos.homeos_notification;

DROP INDEX IF EXISTS homeos.homeos_dynamic_read_family_member_idx;
DROP INDEX IF EXISTS homeos.homeos_dynamic_read_dyn_member_uidx;
DROP TABLE IF EXISTS homeos.homeos_dynamic_read;

DROP INDEX IF EXISTS homeos.homeos_dynamic_family_code_at_idx;
DROP INDEX IF EXISTS homeos.homeos_dynamic_family_at_idx;
DROP TABLE IF EXISTS homeos.homeos_dynamic;

DROP INDEX IF EXISTS homeos.idx_homeos_family_module_family;
DROP INDEX IF EXISTS homeos.homeos_family_module_family_code_uidx;
DROP TABLE IF EXISTS homeos.homeos_family_module;

COMMIT;
