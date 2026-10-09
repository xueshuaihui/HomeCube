-- finance 序列 0018：负债对象实体表
-- PRD §4.7: Liability entity for mortgages, loans, and other debts
-- 支持独立负债对象管理(房贷、私人借款等)，补充finance_asset_liability_report快照表的明细来源

-- ============================================================================
-- finance_liability: 负债对象实体表
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_liability (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    kind TEXT NOT NULL, -- "mortgage", "loan", "credit_card", "other"
    amount_cents BIGINT NOT NULL, -- 负债金额（cents）
    counterparty TEXT, -- 债权人/出借方
    description TEXT, -- 负债描述
    due_date DATE, -- 到期日（可选）
    status TEXT NOT NULL DEFAULT 'active', -- "active", "paid_off", "cancelled"
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

CREATE INDEX IF NOT EXISTS idx_finance_liability_family_id ON finance.finance_liability(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_liability_status ON finance.finance_liability(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_liability_kind ON finance.finance_liability(kind) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_liability_deleted_at ON finance.finance_liability(deleted_at);

-- Check constraint: amount must be non-negative
ALTER TABLE finance.finance_liability ADD CONSTRAINT chk_finance_liability_amount CHECK (amount_cents >= 0);

-- Check constraint: status must be one of the allowed values
ALTER TABLE finance.finance_liability ADD CONSTRAINT chk_finance_liability_status CHECK (status IN ('active', 'paid_off', 'cancelled'));

-- Check constraint: kind must be one of the allowed values
ALTER TABLE finance.finance_liability ADD CONSTRAINT chk_finance_liability_kind CHECK (kind IN ('mortgage', 'loan', 'credit_card', 'other'));
