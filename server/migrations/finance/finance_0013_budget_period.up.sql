-- +migrate Up
-- finance_0013_budget_period: Add finance_budget_periods table for budget period management

CREATE TABLE finance.finance_budget_periods (
    id UUID PRIMARY KEY,
    family_id UUID NOT NULL,
    name VARCHAR(50) NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP,
    deleted_by UUID
);

CREATE INDEX idx_finance_budget_period_family ON finance.finance_budget_periods(family_id);
