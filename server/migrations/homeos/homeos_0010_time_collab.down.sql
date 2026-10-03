-- Migration: 0010_time_collab (down)
-- Purpose: Roll back the four 「时间与协作对象」 tables created by 0010 and nothing else.
-- 反向于 up：先删 up 里最后建的表，逐表先 DROP INDEX 再 DROP TABLE（照 0007/0009 的 down 写法）。
-- 表上的内联约束（homeos_votes_options 的 CHECK、homeos_board_messages_target_pair_chk）随
-- DROP TABLE 一起消失，Postgres 不为它们留独立对象，故不单独 DROP CONSTRAINT。
-- 本支不动任何其他迁移的对象：0007 的 homeos_dynamic*/homeos_notification/homeos_family_module、
-- 0008 的 homeos_due_registration、0009 的 homeos_audit_log 与它加的两列、0006 的身份族表，
-- 一张、一列、一个索引都不碰（判据「down 之后其他表仍存在」）。
-- 逐条 IF EXISTS，可整支重放。

BEGIN;

DROP INDEX IF EXISTS homeos.homeos_votes_family_member_idx;
DROP INDEX IF EXISTS homeos.homeos_votes_family_deadline_idx;
DROP TABLE IF EXISTS homeos.homeos_votes;

DROP INDEX IF EXISTS homeos.homeos_board_messages_parent_idx;
DROP INDEX IF EXISTS homeos.homeos_board_messages_target_idx;
DROP INDEX IF EXISTS homeos.homeos_board_messages_family_member_idx;
DROP INDEX IF EXISTS homeos.homeos_board_messages_family_created_idx;
DROP TABLE IF EXISTS homeos.homeos_board_messages;

DROP INDEX IF EXISTS homeos.homeos_reminders_source_uidx;
DROP INDEX IF EXISTS homeos.homeos_reminders_calendar_event_idx;
DROP INDEX IF EXISTS homeos.homeos_reminders_family_active_trigger_idx;
DROP INDEX IF EXISTS homeos.homeos_reminders_family_owner_trigger_idx;
DROP TABLE IF EXISTS homeos.homeos_reminders;

DROP INDEX IF EXISTS homeos.homeos_todos_source_uidx;
DROP INDEX IF EXISTS homeos.homeos_todos_parent_idx;
DROP INDEX IF EXISTS homeos.homeos_todos_family_due_open_idx;
DROP INDEX IF EXISTS homeos.homeos_todos_family_archived_idx;
DROP INDEX IF EXISTS homeos.homeos_todos_family_member_idx;
DROP INDEX IF EXISTS homeos.homeos_todos_family_assigned_due_idx;
DROP TABLE IF EXISTS homeos.homeos_todos;

COMMIT;
