-- finance 序列 0006：预算管理（finance_budget）与账单管理（finance_bill）
-- PRD 10.4：预算周期是对象、period 是查询参数，服务端不得为凑出季/年档的数而折算预算
-- 两张表均包含：uuid id, family_id, version, soft-delete columns
-- Amounts 存储为 bigint (cents)，禁止 float

-- ============================================================================
-- finance_budget: Budget plans for families by category and period
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_budget (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    category_id UUID NOT NULL,
    amount_cents BIGINT NOT NULL, -- budget amount in cents
    period TEXT NOT NULL, -- 'monthly', 'quarterly', 'yearly'
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_finance_budget_family_id ON finance.finance_budget(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_budget_category_id ON finance.finance_budget(category_id);
CREATE INDEX IF NOT EXISTS idx_finance_budget_period ON finance.finance_budget(period) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_budget_deleted_at ON finance.finance_budget(deleted_at);

-- ============================================================================
-- finance_bill: Bills/invoices with due dates and payment tracking
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_bill (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    payee_id UUID NOT NULL,
    amount_cents BIGINT NOT NULL, -- bill amount in cents
    due_at TIMESTAMPTZ NOT NULL,
    paid_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'pending', -- 'pending', 'paid', 'overdue'
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_finance_bill_family_id ON finance.finance_bill(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_bill_payee_id ON finance.finance_bill(payee_id);
CREATE INDEX IF NOT EXISTS idx_finance_bill_status ON finance.finance_bill(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_bill_due_at ON finance.finance_bill(due_at ASC) WHERE deleted_at IS NULL AND status = 'pending';
CREATE INDEX IF NOT EXISTS idx_finance_bill_deleted_at ON finance.finance_bill(deleted_at);

-- Status check constraint
ALTER TABLE finance.finance_bill ADD CONSTRAINT chk_finance_bill_status CHECK (status IN ('pending', 'paid', 'overdue'));
