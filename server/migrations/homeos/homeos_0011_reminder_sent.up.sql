-- Migration: 0011_reminder_sent
-- Purpose: Create the deduplication table for the due-trigger scanner, which tracks which
--          reminders have already been sent to prevent duplicate homeos.reminder.fired events.
-- Why this table exists: the scanner runs periodically (default every 1 minute) and queries
--          homeos_due_registration for items whose due_at falls within a time window. Without
--          this table, every scan would produce a new outbox row for the same registration,
--          leading to duplicate notifications. The unique index on (registration_id, fire_date)
--          enforces idempotency at the database level.
-- Per PRD 3.4.6「幂等」, contracts/events/homeos.yaml homeos.reminder.fired, and the due-trigger
--     scanner implementation (internal/consumer/due_trigger_scanner.go).
-- 全部语句 IF NOT EXISTS，可整支重放（判据「重放幂等」，见 0001 头注）。

BEGIN;

-- ---------------------------------------------------------------------------
-- homeos.homeos_reminder_sent
-- 列集合 = 去重所需的最小集：
--   id             uuid PRIMARY KEY        -- 本行自身的 id（审计用）
--   registration_id uuid NOT NULL          -- homeos_due_registration.id，指明是哪一条到期注册
--   fire_date      date NOT NULL           -- 触发时刻的日历日期（UTC），与契约 homeos.reminder.fired
--                                          -- 的 fire_date 字段一致（contracts/events/homeos.yaml:175）
--   fired_at       timestamptz NOT NULL    -- 实际触发时刻（用于审计：什么时候发的提醒）
--   created_at     timestamptz NOT NULL    -- 行创建时刻
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_reminder_sent (
    id              uuid         PRIMARY KEY,
    registration_id uuid         NOT NULL,
    fire_date       date         NOT NULL,
    fired_at        timestamptz  NOT NULL,
    created_at      timestamptz  NOT NULL DEFAULT now()
);

-- 唯一索引：同一注册对象的同一天只能触发一次提醒。这是幂等的核心保障 —— 即使扫描仪跑多次、
-- 或者多个实例并发运行，数据库层也会拒绝重复插入。date 类型而非 timestamptz 是因为契约的
-- fire_date 是 date（homeos.yaml:175），且业务语义上「一天一次」比「某一秒一次」更合理。
CREATE UNIQUE INDEX IF NOT EXISTS homeos_reminder_sent_reg_fire_uidx
    ON homeos.homeos_reminder_sent (registration_id, fire_date);

-- 按 registration_id 的索引：查询「某注册对象是否已发送」时用到（fireReminderWithCheck 的
-- SELECT COUNT(*) WHERE registration_id = ? AND fire_date = ?）。虽然上面的唯一索引已经覆盖
-- 了这个查询模式，但显式声明有助于优化器选择。
CREATE INDEX IF NOT EXISTS homeos_reminder_sent_registration_idx
    ON homeos.homeos_reminder_sent (registration_id);

COMMENT ON COLUMN homeos.homeos_reminder_sent.registration_id IS
    '指向 homeos_due_registration.id，标识哪一条到期注册触发了提醒';

COMMENT ON COLUMN homeos.homeos_reminder_sent.fire_date IS
    '触发时刻的日历日期（UTC），与契约 homeos.reminder.fired 的 fire_date 字段一致';

COMMENT ON COLUMN homeos.homeos_reminder_sent.fired_at IS
    '实际触发提醒的时刻（timestamptz），用于审计和排查';

COMMIT;
