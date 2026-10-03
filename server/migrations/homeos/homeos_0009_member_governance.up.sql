-- Migration: 0009_member_governance
-- Purpose: Three things the 成员/面配置 write paths need and 0006 did not carry:
--          ① homeos_families.pver（权限版本号）; ② homeos_invitations 的撤销痕迹两列;
--          ③ homeos_audit_log（PRD 21.5 的审计日志落库点，本卡只写不读）。
-- Per PRD 15.6（pver 与缓存失效）、17.8（面变更即 pver+1 并发 homeos.family.module.updated）、
--     3.7「DELETE /family/invites/{id}：撤销邀请（三态链接同时失效），落审计」、
--     21.5「审计事件九类，审计日志落 svc-homeos，字段带 code」、15.5「越权尝试全部落审计日志」。
-- 全部语句 IF NOT EXISTS / 先判断再加，可整支重放（判据「重放幂等」，见 0001 头注）。
-- 本支只动 homeos schema 自己的对象。

BEGIN;

-- ---------------------------------------------------------------------------
-- ① homeos_families.pver
-- 契约 /auth/login 与 /members/snapshot 都下发 pver，PRD 15.6 要求「权限变更 pver+1 → 各服务
-- 鉴权缓存失效」，17.8 把「面开通/停用」也列为 pver 的自增源。0006 建家庭表时没有本列，
-- 于是那两处契约字段没有真源——本支把它补上，而不是让 handler 返回一个写死的 1。
-- NOT NULL DEFAULT 1：存量行（若有）从 1 起算；PRD 未规定起始值，取 1 与 version 列同规（§2.2）。
ALTER TABLE homeos.homeos_families
    ADD COLUMN IF NOT EXISTS pver bigint NOT NULL DEFAULT 1;

COMMENT ON COLUMN homeos.homeos_families.pver IS
    '本家庭的权限版本号（PRD 15.6、17.8）。自增源：成员角色变更、成员增减、面启用/停用、邀请撤销。';

-- ---------------------------------------------------------------------------
-- ② homeos_invitations 的撤销痕迹
-- PRD 3.7 的撤销动作（DELETE /family/invites/{id}）要求「三态链接同时失效」并落审计，但 0006 的
-- status CHECK 只有 pending/accepted/expired 三值——三态是 PRD 3.4.1 的定版口径，本支不改它。
-- 因此撤销落成本表的两列：status 置 expired（失效，accept 路径即拒绝），revoked_at/revoked_by 记录
-- 「这是人撤销的，不是到点的」，两者共同供 GET /family/invites 与审计查询区分呈现。
-- 上报：定版文本没有给「撤销」一个状态值，本支按「不改既有 CHECK」的约束把它做成 status + 两列痕迹。
ALTER TABLE homeos.homeos_invitations
    ADD COLUMN IF NOT EXISTS revoked_at timestamptz,
    ADD COLUMN IF NOT EXISTS revoked_by uuid;

-- 邀请列表按 (家庭, 状态) 取；0006 只建了 code 与 status 两个单列索引。
CREATE INDEX IF NOT EXISTS idx_homeos_invitations_family_status
    ON homeos.homeos_invitations (family_id, status);

-- ---------------------------------------------------------------------------
-- ③ homeos.homeos_audit_log
-- PRD 21.5 只写了「审计事件九类 + 审计日志落 svc-homeos + 跨面尝试字段带 code」，
-- 没有定义列集合（上报的定版缺口）。下面的列因此逐条挂在文档语句上：
--   family_id        -- 22.5 第 2 道与 §2.2：底座表按家庭切；434 行「按家庭维度的审计查询」。
--   code             -- 21.5「跨面尝试也要记，字段带 code」；取值为 registry 的七个 code 之一。
--   event            -- 21.5 的九类，逐类一个标识符（英文标识是本卡的命名，类目的是文档的）。
--   actor_member_id  -- 「本人可读自己触发的条目」（3.7 audit-events 行）需要这个锚点。
--   target/ref       -- 「联动对账报表」与 15.5 的越权呈现需要指向被作用对象。
--   result/reason    -- 15.5「越权尝试全部落审计」= denied 行；连续 5 次触发通知管理员要按结果计数。
--   occurred_at      -- 审计时刻（不是落库时刻：跨服务上报有延迟）。
-- 本卡只写不读：读接口 GET /api/homeos/audit-events（3.7）属管理台卡片，不在本卡接口清单里。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_audit_log (
    id              uuid        PRIMARY KEY,
    -- 会话家庭。跨家庭尝试的「被尝试家庭」记在 target_family_id，本列始终是令牌的 family_id，
    -- 因为 15.2 规定一切判定以令牌内的家庭为界。
    family_id       uuid,
    target_family_id uuid,
    -- 归属面 code（16.1）。底座自身的动作（登录、成员治理）带 homeos；可空是因为
    -- 早期上报可能只有 service 侧上下文，NOT NULL 会把审计写成丢行。
    code            varchar(20),
    event           varchar(40) NOT NULL CHECK (event IN (
                        'login',                  -- 21.5 第 1 类：登录
                        'permission_change',      -- 第 2 类：权限变更（角色、面启用/停用）
                        'cross_family_attempt',   -- 第 3 类：跨家庭访问尝试
                        'export',                 -- 第 4 类：导出
                        'delete',                 -- 第 5 类：删除（含成员移除、邀请撤销）
                        'l3_read',                -- 第 6 类：L3 读取
                        'rule_toggle',            -- 第 7 类：规则启停
                        'dead_letter_replay',     -- 第 8 类：死信重放
                        'on_behalf_write'         -- 第 9 类：代管写入（on_behalf_of）
                    )),
    -- 动作者：成员 id 优先（家庭内可指认），无账号/未落成员时只有账号 id。两列都可空，
    -- 空即「系统或未知主体」，15.5 的越权条目常常正是这种。
    actor_member_id uuid,
    actor_user_id   uuid,
    action          varchar(30),
    entity          varchar(50),
    entity_id       uuid,
    result          varchar(10) NOT NULL DEFAULT 'allowed' CHECK (result IN ('allowed', 'denied')),
    reason          text,
    ip              varchar(64),
    user_agent      varchar(500),
    created_at      timestamptz NOT NULL DEFAULT now(),
    occurred_at     timestamptz NOT NULL DEFAULT now()
);

-- 434 行的读形态：按家庭 + 类过滤、按时间倒序。
CREATE INDEX IF NOT EXISTS homeos_audit_log_family_event_idx
    ON homeos.homeos_audit_log (family_id, event, occurred_at DESC);

-- 「本人可读自己触发的条目」。
CREATE INDEX IF NOT EXISTS homeos_audit_log_actor_idx
    ON homeos.homeos_audit_log (actor_member_id, occurred_at DESC);

COMMIT;
