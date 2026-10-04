-- +goose Up
-- PRD 15.4 要求的两个字段：记账人归属与 L3 可见性。
--
-- 为什么 0001 里没有、而代码却在用：
--   · handler.ListTransactions 用 `tx.Visibility == "private"` 做 L3 过滤
--     （私密流水只对作者与 owner 可见），用 `tx.CreatedBy` 判作者；
--   · handler.DeleteTransaction 用 `tx.CreatedBy` 判「只有作者或 owner 能删」（PRD 15.3）。
-- 这两处逻辑依赖本迁移新增的两列。0001 的 finance_transaction 只有 17 列
-- （deleted_by 有、created_by 没有；visibility 根本没有），于是 finance 服务
-- **在本机就编译不过**（`tx.Visibility undefined (type model.FinanceTransaction has no field...)`），
-- L3 可见性与「作者可删」这两条权限规则也无法真正生效。
--
-- 口径：
--   · created_by = 记账人的 **member_id**（不是 user_id / account_id）——
--     PRD 15.3/15.4 的角色矩阵按家庭成员（member）判定，与 authz.Session.MemberID 同源；
--     旧数据一律留空（NULL），此时 L3 判定退化���「非作者」，即只有 owner 能删/能看私密，
--     这是更保守的一侧，符合 fail closed（PRD 15.2）。
--   · visibility 默认 'shared'：不改变任何既有行的可见性，
--     因此本迁移对存量数据是零行为变更，只有新写入才能选 'private'。

ALTER TABLE finance.finance_transaction
    ADD COLUMN IF NOT EXISTS created_by UUID;

ALTER TABLE finance.finance_transaction
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'shared';

-- visibility 的取值域用 CHECK 约束收口：拼错成 'pravate' 会让 L3 过滤静默失效
-- （既不是 'private' 就不进过滤分支，私密流水对所有人可见），这是权限漏洞。
ALTER TABLE finance.finance_transaction
    DROP CONSTRAINT IF EXISTS ck_finance_transaction_visibility;
ALTER TABLE finance.finance_transaction
    ADD CONSTRAINT ck_finance_transaction_visibility
    CHECK (visibility IN ('shared', 'private'));

-- 列表查询的过滤条件是 `family_id = ? AND visibility <> 'private'`，
-- 走 (family_id) 现有索引即可；这里额外给 created_by 一个索引，供「我记的账」这类查询用。
CREATE INDEX IF NOT EXISTS idx_finance_transaction_created_by
    ON finance.finance_transaction (created_by)
    WHERE created_by IS NOT NULL AND deleted_at IS NULL;

COMMENT ON COLUMN finance.finance_transaction.created_by IS
    '记账人的 member_id（PRD 15.3/15.4）。NULL = 未知作者，此时仅 owner 可删/可看私密（fail closed）';
COMMENT ON COLUMN finance.finance_transaction.visibility IS
    'L3 可见性：shared = 家庭内可见；private = 仅作者与 owner 可见（PRD 15.4）';
