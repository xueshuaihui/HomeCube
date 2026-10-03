-- +migrate Down
-- finance_0013_budget_period: Remove finance_budget_periods table

DROP INDEX IF EXISTS finance.idx_finance_budget_period_family;
DROP TABLE IF EXISTS finance.finance_budget_periods;
