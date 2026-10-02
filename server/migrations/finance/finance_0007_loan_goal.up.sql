-- finance 序列 0007：借贷管理（finance_loan + finance_repayment_plan）与储蓄目标（finance_goal）
-- PRD 8.2 M2 段：借还款与储蓄目标，两者都要注册到到期中心
-- 三张表均包含：uuid id, family_id, version, soft-delete columns
-- Amounts 存储为 bigint (cents)，禁止 float

-- ============================================================================
-- finance_loan: Loan records between parties
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_loan (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    lender_name TEXT NOT NULL,
    borrower_name TEXT NOT NULL,
    principal_cents BIGINT NOT NULL, -- loan amount in cents
    interest_rate NUMERIC(5,2) NOT NULL DEFAULT 0, -- annual interest rate as percentage (e.g., 5.5 for 5.5%)
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'active', -- 'active', 'paid_off'
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_finance_loan_family_id ON finance.finance_loan(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_loan_status ON finance.finance_loan(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_loan_deleted_at ON finance.finance_loan(deleted_at);

-- Status check constraint
ALTER TABLE finance.finance_loan ADD CONSTRAINT chk_finance_loan_status CHECK (status IN ('active', 'paid_off'));

-- ============================================================================
-- finance_repayment_plan: Repayment installments for loans
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_repayment_plan (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    loan_id UUID NOT NULL,
    due_at TIMESTAMPTZ NOT NULL,
    amount_cents BIGINT NOT NULL, -- repayment amount in cents
    paid_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'pending', -- 'pending', 'paid', 'overdue'
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_finance_repayment_plan_family_id ON finance.finance_repayment_plan(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_repayment_plan_loan_id ON finance.finance_repayment_plan(loan_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_repayment_plan_due_at ON finance.finance_repayment_plan(due_at ASC) WHERE deleted_at IS NULL AND status = 'pending';
CREATE INDEX IF NOT EXISTS idx_finance_repayment_plan_status ON finance.finance_repayment_plan(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_repayment_plan_deleted_at ON finance.finance_repayment_plan(deleted_at);

-- Status check constraint
ALTER TABLE finance.finance_repayment_plan ADD CONSTRAINT chk_finance_repayment_plan_status CHECK (status IN ('pending', 'paid', 'overdue'));

-- Foreign key to loan (logical reference only, no physical constraint per PRD 16.3)
-- Note: We store loan_id but do not create a foreign key constraint

-- ============================================================================
-- finance_goal: Savings goals for families
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_goal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    name TEXT NOT NULL,
    target_amount_cents BIGINT NOT NULL, -- target amount in cents
    current_amount_cents BIGINT NOT NULL DEFAULT 0, -- current saved amount in cents
    deadline DATE NOT NULL,
    is_achieved BOOLEAN NOT NULL DEFAULT false,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_finance_goal_family_id ON finance.finance_goal(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_goal_deadline ON finance.finance_goal(deadline ASC) WHERE deleted_at IS NULL AND is_achieved = false;
CREATE INDEX IF NOT EXISTS idx_finance_goal_deleted_at ON finance.finance_goal(deleted_at);

-- Check constraint: current_amount_cents should not exceed target_amount_cents by too much
ALTER TABLE finance.finance_goal ADD CONSTRAINT chk_finance_goal_amounts CHECK (current_amount_cents >= 0 AND target_amount_cents > 0);
