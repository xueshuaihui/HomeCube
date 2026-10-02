-- finance 序列 0008：分账结算（AA 零误差分账）
-- PRD §8.2 M2 段：分账与净资产管理
-- 核心要求：分账金额总和必须等于原流水金额（bigint cents 保证无浮点误差）
-- 状态机：draft → pending → settled，不可逆

-- ============================================================================
-- finance_split_settlement: 分账结算记录
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_split_settlement (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    transaction_id UUID NOT NULL, -- 原始流水 ID
    status TEXT NOT NULL DEFAULT 'draft', -- 'draft', 'pending', 'settled'
    total_amount_cents BIGINT NOT NULL, -- 分账总金额（应等于 transaction.amount_cents）
    settled_at TIMESTAMPTZ, -- 完成分账时间
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

CREATE INDEX IF NOT EXISTS idx_finance_split_settlement_family_id ON finance.finance_split_settlement(family_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_split_settlement_transaction_id ON finance.finance_split_settlement(transaction_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_split_settlement_status ON finance.finance_split_settlement(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_split_settlement_deleted_at ON finance.finance_split_settlement(deleted_at);

-- Status check constraint: draft → pending → settled, irreversible
ALTER TABLE finance.finance_split_settlement ADD CONSTRAINT chk_finance_split_settlement_status CHECK (status IN ('draft', 'pending', 'settled'));

-- Check constraint: total_amount_cents must be positive or negative but not zero
ALTER TABLE finance.finance_split_settlement ADD CONSTRAINT chk_finance_split_settlement_amount CHECK (total_amount_cents != 0);

-- ============================================================================
-- finance_participant: 分账参与方
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_participant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    settlement_id UUID NOT NULL, -- 关联的分账结算 ID
    account_id UUID NOT NULL, -- 参与方账户 ID
    share_ratio NUMERIC(5,4) NOT NULL, -- 分摊比例（0.0000 - 1.0000）
    share_amount_cents BIGINT NOT NULL, -- 分摊金额（cents）
    status TEXT NOT NULL DEFAULT 'pending', -- 'pending', 'paid'
    paid_at TIMESTAMPTZ, -- 实际支付时间
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

CREATE INDEX IF NOT EXISTS idx_finance_participant_settlement_id ON finance.finance_participant(settlement_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_participant_account_id ON finance.finance_participant(account_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_participant_status ON finance.finance_participant(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_participant_deleted_at ON finance.finance_participant(deleted_at);

-- Status check constraint
ALTER TABLE finance.finance_participant ADD CONSTRAINT chk_finance_participant_status CHECK (status IN ('pending', 'paid'));

-- Check constraint: share_ratio must be between 0 and 1
ALTER TABLE finance.finance_participant ADD CONSTRAINT chk_finance_participant_ratio CHECK (share_ratio >= 0 AND share_ratio <= 1);
