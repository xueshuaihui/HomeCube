-- finance 序列 0010：资产负债报表快照
-- PRD §8.2 M2 段：资产负债报表
-- 按 period 聚合，total_assets = SUM(所有账户余额 + 储蓄目标当前值)
-- total_liabilities = SUM(贷款本金 + 信用卡欠款）
-- net_worth = assets - liabilities

-- ============================================================================
-- finance_asset_liability_report: 资产负债报表快照
-- ============================================================================
CREATE TABLE IF NOT EXISTS finance.finance_asset_liability_report (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    period TEXT NOT NULL, -- 报表周期（格式：YYYY-MM）
    total_assets_cents BIGINT NOT NULL DEFAULT 0, -- 总资产（cents）
    total_liabilities_cents BIGINT NOT NULL DEFAULT 0, -- 总负债（cents）
    net_worth_cents BIGINT NOT NULL, -- 净资产（cents）= assets - liabilities
    snapshot_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- 快照生成时间
    details JSONB, -- 详细构成（可选，用于追溯）
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

CREATE INDEX IF NOT EXISTS idx_finance_asset_liability_report_family_id ON finance.finance_asset_liability_report(family_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uk_finance_asset_liability_report_family_period ON finance.finance_asset_liability_report(family_id, period) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_asset_liability_report_snapshot_at ON finance.finance_asset_liability_report(snapshot_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_finance_asset_liability_report_deleted_at ON finance.finance_asset_liability_report(deleted_at);

-- Check constraint: net_worth must equal assets - liabilities
ALTER TABLE finance.finance_asset_liability_report ADD CONSTRAINT chk_finance_asset_liability_net_worth CHECK (net_worth_cents = total_assets_cents - total_liabilities_cents);

-- Check constraint: amounts can be negative for liabilities but assets should be non-negative
ALTER TABLE finance.finance_asset_liability_report ADD CONSTRAINT chk_finance_asset_liability_amounts CHECK (total_assets_cents >= 0 AND total_liabilities_cents >= 0);
