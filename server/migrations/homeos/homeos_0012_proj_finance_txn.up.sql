-- homeos 序列 0012：投影表 homeos_proj_finance 的月桶「重算源」（§2.3 第 6 行、§六、PRD 10.4）
--
-- 为什么要这张表：§六「{code}_proj_{src} … 只允许本服务订阅器写入，可随时全量重建」+ PRD 10.4「幂等：
-- 同一事件重投不得重复计入」。首页那一格读的是 homeos_proj_finance 的月桶聚合（income_cents/expense_cents），
-- 而消费方（svc-homeos 订阅器）读不到 finance 自己的 finance_transaction（§2.3「无跨 schema 读、无跨服务事务」）。
-- 于是聚合桶要能「重算」而不是「增量」——删除一笔流水必须把它的贡献撤掉——订阅器就得在自己的 schema 里
-- 留一份流水级副本作重算源。这张表就是那份副本：交易级、可按 (family_id, bucket_month) 重算出 0004 的月桶。
--
-- 命名口径：{code}_proj_* 命中定版 ㉔ 后缀白名单（§10.2 第 2 道），前缀 homeos_ = schema homeos 的 code。
-- 它是 0004 那张投影表的前置明细（同为 finance 域的本地投影、同由本服务订阅器独占写入），不是第二数据源：
-- 全表可由 finance.transaction.* 事件流随时重建，删除任何一笔都能重算回正确的月桶。
--
-- 引用他域对象一律「只存 id，无物理约束」（§2.2「不存在跨服务外键」），所以 transaction_id 不加 REFERENCES。
CREATE TABLE IF NOT EXISTS homeos.homeos_proj_finance_txn (
    -- finance.yaml:26 的 transaction_id，也是三支交易事件 business_id「{transaction_id}」的那段。
    -- 主键 = 消费幂等的天然锚：同一 transaction.created 无论重投几次，都只 upsert 这一行、不追加第二行。
    transaction_id uuid    PRIMARY KEY,
    -- §2.2「索引第一列固定 family_id」；投影的统计范围恒为会话家庭（15.2）。
    family_id      uuid    NOT NULL,
    -- 该流水归属的自然月桶首日（家庭时区口径的 YYYY-MM-01，0004「bucket_month 取该自然月的首日」）。
    -- 冗余存一份，重算月桶时按它分组，避免每次再从 occurred_at 求月份。
    bucket_month   date    NOT NULL,
    -- 单笔归一化后的收支两翼，均为「分」的非负幅值（§2.2「金额一律 amount_cents bigint；禁止浮点」）。
    -- finance 侧支出以负数存（svc-finance model/finance.go「positive for income, negative for expense」），
    -- 本表把支出的幅值放 expense_cents、收入放 income_cents，转账两列皆 0——聚合表读到的就是非负数，
    -- faces.go 的「本周期支出 ¥X」不再被二次取负。
    income_cents   bigint  NOT NULL DEFAULT 0,
    expense_cents  bigint  NOT NULL DEFAULT 0,
    -- 流水实际发生时刻（finance.yaml:31 occurred_at），bucket_month 由它派生，留原值便于重算/对账。
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    -- 本地写入时刻，与 0004 的 updated_at 同口径（as_of 的新鲜度源，12.3「截至 HH:MM」）。
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- 重算月桶的分组/过滤键；第一列 family_id 满足 §2.2「索引第一列固定 family_id」。
CREATE INDEX IF NOT EXISTS homeos_proj_finance_txn_family_month_idx
    ON homeos.homeos_proj_finance_txn (family_id, bucket_month);
