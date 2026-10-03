-- +migrate Up
-- finance_0012_recurring_rule: Add finance_recurring_rules table for recurring transactions (PRD 4.7, 18.3#4)

CREATE TABLE finance.finance_recurring_rules (
    id UUID PRIMARY KEY,
    family_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) NOT NULL CHECK (type IN ('expense', 'income')),
    amount_cents BIGINT NOT NULL,
    account_id UUID NOT NULL,
    category_id UUID NOT NULL,
    cycle VARCHAR(20) NOT NULL CHECK (cycle IN ('daily', 'weekly', 'monthly', 'yearly')),
    start_date TIMESTAMP NOT NULL,
    end_date TIMESTAMP,
    last_executed_at TIMESTAMP,
    next_execute_at TIMESTAMP NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    description TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP,
    deleted_by UUID
);

CREATE INDEX idx_finance_recurring_family ON finance.finance_recurring_rules(family_id);
CREATE INDEX idx_finance_recurring_next ON finance.finance_recurring_rules(next_execute_at);
