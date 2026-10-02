-- finance 序列 0003：底座运行表族之「同步底座」两支（§2.3 的第 4、5 行，§五）
-- 后缀 _change_log / _idempotency 命中定版 ㉔ 白名单；前缀 finance_ = schema finance 的 code。

-- ---------------------------------------------------------------------------
-- finance.finance_change_log
-- §五「变更日志：业务写操作在事务内向 finance_change_log(lsn bigserial, family_id, entity, entity_id, op, version) 追加，
--       由 packages/sync 的 repo 基类完成，业务代码不记得写」
-- §五「delta：GET /api/{code}/sync/changes?family_id&since_lsn&limit，每服务一条游标」
-- §六「动态流与搜索共用 {code}_change_log，避免两套变更捕获」
-- 列集合就是 §五 那六个字，不增不减；LSN 的单调性由 bigserial 提供，游标语义要求它唯一 -> PRIMARY KEY。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS finance.finance_change_log (
    lsn       bigserial   PRIMARY KEY,   -- §五 lsn bigserial；§五 since_lsn 游标
    family_id uuid        NOT NULL,      -- §五 family_id；§3.4.4/15.2 一切判定以会话家庭为界
    entity    text        NOT NULL,      -- §五 entity
    entity_id uuid        NOT NULL,      -- §五 entity_id；§2.2「业务表必备列：id uuid primary key」
    op        text        NOT NULL,      -- §五 op（取值文档未枚举 -> 不加 CHECK）
    version   bigint      NOT NULL       -- §五 version；§2.2「version bigint not null default 1」的乐观锁版本
);

-- §2.2「索引第一列固定 family_id」+ §五 delta 查询条件 (family_id, since_lsn, limit)。
CREATE INDEX IF NOT EXISTS finance_change_log_family_lsn_idx ON finance.finance_change_log (family_id, lsn);

-- ---------------------------------------------------------------------------
-- finance.finance_idempotency
-- §2.3「finance_idempotency | client_request_id 去重」
-- §五「幂等：所有写接口要求 client_request_id，
--       finance_idempotency(key, family_id, request_hash, response_snapshot, created_at) 唯一索引，
--       重放返回首次响应而非重复执行」
-- PRD 14.7「所有写接口必须携带 client_request_id 幂等键」
-- 列集合就是 §五 那五个字；key 即 client_request_id（§2.3 的用途列写明）。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS finance.finance_idempotency (
    key               text        NOT NULL,   -- §五 key == §2.3 client_request_id == PRD 14.7 幂等键
    family_id         uuid        NOT NULL,   -- §五 family_id
    request_hash      text        NOT NULL,   -- §五 request_hash
    -- §五「重放返回首次响应」-> 必须存首次响应体；文档未给类型，本卡取 jsonb -> 见报告。
    response_snapshot jsonb,                  -- §五 response_snapshot
    created_at        timestamptz NOT NULL DEFAULT now()   -- §五 created_at
);

-- §五「唯一索引」。文档没写这条唯一索引的列集合，本卡按 §2.2「索引第一列固定 family_id」
-- 与 15.2「判定始终以 token 内的 family_id 为界」落 (family_id, key) -> 已列入报告待回写。
CREATE UNIQUE INDEX IF NOT EXISTS finance_idempotency_family_key_uidx ON finance.finance_idempotency (family_id, key);
