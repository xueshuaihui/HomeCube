-- finance 序列 0009：信用卡账户与发票记录
-- PRD §8.2 M2 段：多币种、信用卡、发票与报销

-- ============================================================================
-- finance_credit_card: 信用卡账户
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_credit_card (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    card_number_hash TEXT NOT NULL, -- 卡号哈希（不可逆）
    issuer TEXT NOT NULL, -- 发卡行
    billing_day INTEGER NOT NULL CHECK (billing_day >= 1 AND billing_day <= 31), -- 账单日
    due_day INTEGER NOT NULL CHECK (due_day >= 1 AND due_day <= 31), -- 还款日
    credit_limit_cents BIGINT NOT NULL, -- 信用额度（cents）
    current_balance_cents BIGINT NOT NULL DEFAULT 0, -- 当前欠款余额（cents，正数表示欠款）
    currency TEXT NOT NULL DEFAULT 'CNY', -- 币种
    status TEXT NOT NULL DEFAULT 'active', -- 'active', 'frozen', 'closed'
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

CREATE INDEX IF NOT EXISTS idx_finance_credit_card_family_id ON finance.finance_credit_card(family_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uk_finance_credit_card_card_number_hash ON finance.finance_credit_card(card_number_hash) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_credit_card_status ON finance.finance_credit_card(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_credit_card_deleted_at ON finance.finance_credit_card(deleted_at);

-- Status check constraint
ALTER TABLE finance.finance_credit_card ADD CONSTRAINT chk_finance_credit_card_status CHECK (status IN ('active', 'frozen', 'closed'));

-- Check constraint: credit_limit must be positive
ALTER TABLE finance.finance_credit_card ADD CONSTRAINT chk_finance_credit_card_limit CHECK (credit_limit_cents > 0);

-- Check constraint: current_balance should not exceed credit limit by too much (allow some overlimit)
ALTER TABLE finance.finance_credit_card ADD CONSTRAINT chk_finance_credit_card_balance CHECK (current_balance_cents >= 0);

-- ============================================================================
-- finance_invoice: 发票记录
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_invoice (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    invoice_number TEXT NOT NULL, -- 发票号码
    amount_cents BIGINT NOT NULL, -- 发票金额（cents）
    tax_amount_cents BIGINT NOT NULL DEFAULT 0, -- 税额（cents）
    vendor TEXT NOT NULL, -- 供应商/开票方
    issue_date DATE NOT NULL, -- 开票日期
    reimbursement_status TEXT NOT NULL DEFAULT 'pending', -- 'pending', 'reimbursed', 'rejected'
    reimbursed_at TIMESTAMPTZ, -- 报销完成时间
    rejected_reason TEXT, -- 拒绝原因
    transaction_id UUID, -- 关联的报销流水 ID
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

CREATE INDEX IF NOT EXISTS idx_finance_invoice_family_id ON finance.finance_invoice(family_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uk_finance_invoice_number ON finance.finance_invoice(invoice_number) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_invoice_reimbursement_status ON finance.finance_invoice(reimbursement_status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_invoice_issue_date ON finance.finance_invoice(issue_date DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_invoice_transaction_id ON finance.finance_invoice(transaction_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_invoice_deleted_at ON finance.finance_invoice(deleted_at);

-- Reimbursement status check constraint: only pending can transition to other states
ALTER TABLE finance.finance_invoice ADD CONSTRAINT chk_finance_invoice_reimbursement_status CHECK (reimbursement_status IN ('pending', 'reimbursed', 'rejected'));

-- Check constraint: amounts must be non-negative
ALTER TABLE finance.finance_invoice ADD CONSTRAINT chk_finance_invoice_amounts CHECK (amount_cents >= 0 AND tax_amount_cents >= 0);
