-- +goose Up
-- Finance settings table for storing per-family configuration (PRD 4.8)

CREATE TABLE IF NOT EXISTS finance.finance_settings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    
    -- Basic settings
    currency_unit VARCHAR(10) NOT NULL DEFAULT 'CNY',
    decimal_places INTEGER NOT NULL DEFAULT 2 CHECK (decimal_places >= 0 AND decimal_places <= 4),
    
    -- Alert settings
    budget_alert_threshold NUMERIC(5,2) NOT NULL DEFAULT 0.80 CHECK (budget_alert_threshold > 0 AND budget_alert_threshold <= 1.0),
    
    -- Feature toggles
    auto_categorize_enabled BOOLEAN NOT NULL DEFAULT false,
    receipt_ocr_enabled BOOLEAN NOT NULL DEFAULT false,
    voice_input_enabled BOOLEAN NOT NULL DEFAULT false,
    
    -- Metadata
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- 「每家庭唯一且未删除」是部分唯一索引（partial unique index），不是表级 UNIQUE 约束：
-- PostgreSQL 不允许 CONSTRAINT ... UNIQUE (...) WHERE ...，写成表约束会在 DDL 阶段直接报
-- syntax error at or near "WHERE"。口径与 0009/0010 的 uk_* 部分唯一索引一致。
CREATE UNIQUE INDEX IF NOT EXISTS uk_finance_settings_family_id
    ON finance.finance_settings (family_id) WHERE deleted_at IS NULL;

-- Index for faster lookups by family_id
CREATE INDEX idx_finance_settings_family_id ON finance.finance_settings (family_id) WHERE deleted_at IS NULL;

-- Comment on table and columns
COMMENT ON TABLE finance.finance_settings IS 'Per-family finance settings (PRD 4.8)';
COMMENT ON COLUMN finance.finance_settings.currency_unit IS 'Currency unit code (CNY, USD, EUR, JPY, etc.)';
COMMENT ON COLUMN finance.finance_settings.decimal_places IS 'Number of decimal places for amounts (0-4)';
COMMENT ON COLUMN finance.finance_settings.budget_alert_threshold IS 'Budget alert threshold as a fraction (e.g., 0.80 = 80%)';
COMMENT ON COLUMN finance.finance_settings.auto_categorize_enabled IS 'Enable automatic transaction categorization';
COMMENT ON COLUMN finance.finance_settings.receipt_ocr_enabled IS 'Enable receipt OCR recognition';
COMMENT ON COLUMN finance.finance_settings.voice_input_enabled IS 'Enable voice input for transactions';
