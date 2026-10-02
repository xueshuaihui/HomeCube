-- finance 序列 0001：本域 schema 边界（§2.1）
-- §2.1「一个 postgres16 实例，当期两个 schema、两个角色」；
-- §2.1「建 schema 与建账号的动作只发生在 make up 与迁移容器里，不由服务进程在运行期自触发」。
-- 因此序列自己只创建 **自己那一个** schema；账号 hc_finance（§1.3 row 2 校验点「账号名 hc_{code}」）
-- 与 GRANT/ALTER DEFAULT PRIVILEGES/REVOKE 属迁移容器的 bootstrap，不进本序列：
-- §2.1 的 GRANT 块含 PASSWORD 字面量，凭据不入库（§2.1「避免账号密码进应用配置」）。
-- 他域 schema 由他域序列各自负责（§2.2「每服务一条独立序列」），本文件不引用 finance/homeos 之外的对象。
-- IF NOT EXISTS：本序列在已建过 schema 的库上重放必须无副作用（判据「重放幂等」）。

-- golang-migrate 版本表（显式创建，见技术方案 §2.2）
CREATE TABLE IF NOT EXISTS schema_migrations_finance (
    version bigint NOT NULL PRIMARY KEY,
    dirty boolean NOT NULL DEFAULT false
);

CREATE SCHEMA IF NOT EXISTS finance;

-- ============================================================================
-- Finance Core Tables (PRD 4.5, tech plan §8.1)
-- All tables include: uuid id, family_id, version, soft-delete columns
-- Amounts are stored as bigint (cents), never float
-- ============================================================================

-- finance_account: Financial accounts for a family
CREATE TABLE IF NOT EXISTS finance.finance_account (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL, -- e.g., 'cash', 'bank', 'credit_card'
    balance BIGINT NOT NULL DEFAULT 0, -- amount in cents
    is_archived BOOLEAN NOT NULL DEFAULT false,
    archived_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_finance_account_family_id ON finance.finance_account(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_account_deleted_at ON finance.finance_account(deleted_at);

-- finance_category: Transaction categories for a family
CREATE TABLE IF NOT EXISTS finance.finance_category (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    name TEXT NOT NULL,
    icon TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT true,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_finance_category_family_id ON finance.finance_category(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_category_deleted_at ON finance.finance_category(deleted_at);

-- finance_transaction: Financial transactions (income/expense/transfer)
CREATE TABLE IF NOT EXISTS finance.finance_transaction (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    type TEXT NOT NULL, -- 'income', 'expense', 'transfer'
    amount_cents BIGINT NOT NULL, -- positive for income, negative for expense
    category_id UUID,
    account_id UUID NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    description TEXT,
    receipt_file_id UUID,
    transfer_group_id UUID,
    client_request_id UUID, -- for idempotency
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

CREATE INDEX IF NOT EXISTS idx_finance_transaction_family_id ON finance.finance_transaction(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_transaction_category_id ON finance.finance_transaction(category_id);
CREATE INDEX IF NOT EXISTS idx_finance_transaction_account_id ON finance.finance_transaction(account_id);
CREATE INDEX IF NOT EXISTS idx_finance_transaction_transfer_group_id ON finance.finance_transaction(transfer_group_id);
CREATE UNIQUE INDEX IF NOT EXISTS uk_finance_transaction_client_request_id ON finance.finance_transaction(client_request_id) WHERE client_request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_finance_transaction_deleted_at ON finance.finance_transaction(deleted_at);
CREATE INDEX IF NOT EXISTS idx_finance_transaction_occurred_at ON finance.finance_transaction(occurred_at DESC, id DESC) WHERE deleted_at IS NULL;

-- finance_ledger: Financial ledgers/books for tracking budgets or periods
CREATE TABLE IF NOT EXISTS finance.finance_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    name TEXT NOT NULL,
    member_ids JSONB NOT NULL, -- array of member UUIDs
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_finance_ledger_family_id ON finance.finance_ledger(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_ledger_deleted_at ON finance.finance_ledger(deleted_at);
