-- Migration: 0008_due_registration
-- Purpose: Create 到期中心's registration table, the write target of the already-shipped
--          consumer (internal/consumer/finance_consumer.go HandleDueRegistered) and the read
--          source of repo.HomeSummaryDueToday (0007's home/summary B 区).
-- Why this branch exists: PRD 14.5 第 4 项 puts 「日历/提醒/待办 + 到期中心 + 16.4 注册契约」 in
--          P1-M1's 底座 scope and 3.8 骨架自检 judges 「到期中心可注册可触发」; the consumer and
--          the home aggregator already read and write this table, so leaving it out of the
--          sequence would keep a live code path pointed at a nonexistent relation.
-- Per PRD 16.4 (到期注册契约), 3.6 (Family/Dynamic 行的 due 语义), 17.2 B 区「今日到期」,
--     contracts/events/finance.yaml finance.due.registered / finance.due.revoked,
--     tech plan §2.2 (业务表必备列 + 索引第一列 family_id) and §六 (无跨服务外键).
-- 全部语句 IF NOT EXISTS，可整支重放（判据「重放幂等」，见 0001 头注）。

BEGIN;

-- ---------------------------------------------------------------------------
-- homeos.homeos_due_registration
-- 列集合 = 消费方实际写入的列 + 契约 finance.due.registered 声明的列，逐列有出处：
--   family_id / source_system / source_id / kind / title / due_at
--     <- HandleDueRegistered 的 Create map；kind 的四值 <- 契约 enum(bill|budget|goal|repayment)。
-- 契约里另有 members: array[uuid]（该到期对象的受益成员集），消费方当前不解析、首页 B 区也不读，
-- 所以本支不建该列：建一列没有写方的数据就是骗契约（判据「不写死假数据」）。已上报，待
-- 「代看到期对象成员」这条需求真的下发时随事件消费方一起加。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_due_registration (
    id            uuid         PRIMARY KEY,
    -- §2.2「索引第一列固定 family_id」：到期触发按家庭切，跨家庭读取一律拒绝（15.2）。
    family_id     uuid         NOT NULL,
    -- 来源域的 code（16.1），不是服务名：home/summary 的 due_today.items[].source_system 直接下发本列。
    source_system varchar(20)  NOT NULL,
    -- 来源域内该业务对象的 id。§六「他域对象只存 id，无物理约束」：本列不加 REFERENCES，
    -- 也不加 NOT NULL —— 消费方写入的 source_id 来自事件，缺它就没有幂等锚点，故 NOT NULL。
    source_id     uuid         NOT NULL,
    -- 受控枚举的出处是 finance.due.registered 的 kind 列。CHECK 而非枚举类型：P2 起其他面也会注册
    -- 自己的到期对象（bill 之外的 kind），届时 ALTER ... ADD CONSTRAINT 比换类型便宜。
    kind          varchar(20)  NOT NULL CHECK (kind IN ('bill', 'budget', 'goal', 'repayment')),
    -- 标题在注册时定稿并随 revocation 之前的每次 due_at 变更重写（消费方的 Updates 列集合）。
    title         varchar(200) NOT NULL,
    -- 到期时刻（timestamptz）：家庭时区在展示层格式化（0007 homeos_dynamic.at 同一取舍）。
    due_at        timestamptz  NOT NULL,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    updated_at    timestamptz  NOT NULL DEFAULT now(),
    -- 软删（本卡的硬规则 3）：finance.due.revoked 是本表唯一的删除路径，撤销动作者是他服务而不是
    -- 某个家庭成员，所以 deleted_by 可空——非空时是执行撤销的成员 id。
    deleted_at    timestamptz,
    deleted_by    uuid
);

-- 消费方的 upsert 锚点就是这三元组（WHERE source_system = ? AND source_id = ? AND kind = ?）。
-- 唯一索引必须部分于 deleted_at IS NULL：撤销后同一对象再次注册（账单重新排期）要能进新的一行，
-- 否则历史撤销行会把槽位永久占住，重放幂等（0001 判据）与「结清后可再注册」同时被破。
CREATE UNIQUE INDEX IF NOT EXISTS homeos_due_registration_source_uidx
    ON homeos.homeos_due_registration (source_system, source_id, kind)
    WHERE deleted_at IS NULL;

-- 到期触发与首页 B 区都是「某家庭 due_at 落在窗口内的一批」：本索引即
-- HomeSummaryDueToday 的 WHERE family_id = ? AND due_at >= ? AND due_at < ? ORDER BY due_at。
CREATE INDEX IF NOT EXISTS homeos_due_registration_family_due_idx
    ON homeos.homeos_due_registration (family_id, due_at ASC)
    WHERE deleted_at IS NULL;

-- 「今日已过期待处理」是 B 区的另一条口径（逾期未清的对象仍要出现在到期中心列表里）。
CREATE INDEX IF NOT EXISTS idx_homeos_due_registration_family
    ON homeos.homeos_due_registration (family_id);

COMMENT ON COLUMN homeos.homeos_due_registration.source_system IS
    '注册该到期对象的域 code（PRD 16.1）。首页 due_today.items[].source_system 即本列。';

COMMIT;
