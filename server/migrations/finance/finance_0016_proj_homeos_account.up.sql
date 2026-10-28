-- +goose Up
-- finance 序列 0016：投影表 finance_proj_homeos 补 account_id 列。
--
-- 为什么需要它：finance.finance_proj_homeos 的唯一读者是 handler/auth.go 的 resolveMember，
-- 它必须按 (family_id, account_id) 解析出**调用者本人**的 member_id（PRD 15.2/15.3/15.4 的
-- L3 作者判定以此为前提）。0004 建的列集（family_id/member_id/role/pver/updated_at）没有
-- account_id，0004 自己的注释已预告「等 homeos 侧投影补齐 account_id 后这里应收窄为
-- family_id + account_id 的精确匹配」—— 本迁移就是那一步的 schema 半边。
--
-- 消费侧：svc-finance internal/consumer 的 homeos.member.created/updated/deleted 订阅器
-- （contracts/events/homeos.yaml:13-66）把消息里的账号归属投影进本列。
-- 契约缺口（已上报，不在本仓单方修改）：homeos.member.created 的 payload_schema 当前没有
-- account_id 字段，消费器对**带**该扩展字段的消息落列，不带的消息 account_id 保持 NULL，
-- resolveMember 据此退化为透传（见 handler/auth.go 文件头口径）。
--
-- 完整性：homeos_members 的 UNIQUE(family_id, user_id)（homeos_0001）决定了「一个账号在一个
-- 家庭里至多一个成员行」，所以这里的镜像同样用部分唯一索引收口（只约束非空 account_id，
-- 无账号的 ward/guest 成员不受影响）。
ALTER TABLE finance.finance_proj_homeos
    ADD COLUMN IF NOT EXISTS account_id uuid;

CREATE UNIQUE INDEX IF NOT EXISTS finance_proj_homeos_family_account_uidx
    ON finance.finance_proj_homeos (family_id, account_id)
    WHERE account_id IS NOT NULL;

COMMENT ON COLUMN finance.finance_proj_homeos.account_id IS
    'homeos.homeos_users.id（token 的 sub）。NULL = 该成员无账号（ward/guest 代管建档），'
    '不参与 resolveMember 的 (family_id, account_id) 精确匹配';
