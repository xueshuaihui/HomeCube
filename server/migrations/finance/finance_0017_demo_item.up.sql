-- +goose Up
-- finance 序列 0017：finance_demo_item（P1 跨服务五步验证的 demo 业务表）。
--
-- contracts/events/finance.yaml:123-138（FROZEN v1.0.0）登记了 finance.demo_item.created
-- 「Demo项创建（P1跨服务链路验证，M2收口时删除）」，此前本域既没有表也没有发布方，
-- 该契约事件无处可发。契约原文同步要求：「M2收口（S19）时必须删除demo表及相关接口与事件」
-- —— 本迁移与 handler/service 的对应接口是**限时工件**，M2 收口卡按契约注释整体删除。
--
-- 列集 = 契约 payload_schema 的字段（demo_item_id/family_id/title/due_at/created_by），
-- 外加 §2.2「业务表必备列」（id uuid primary key、version bigint not null default 1、
-- 软删三列），不发明契约外的业务列。family_id 首列索引按 §2.2「索引第一列固定 family_id」。
CREATE TABLE IF NOT EXISTS finance.finance_demo_item (
    id         uuid        PRIMARY KEY,
    family_id  uuid        NOT NULL,
    title      text        NOT NULL,
    due_at     timestamptz NOT NULL,
    created_by uuid,
    version    bigint      NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    deleted_by uuid
);

CREATE INDEX IF NOT EXISTS finance_demo_item_family_created_idx
    ON finance.finance_demo_item (family_id, created_at);
