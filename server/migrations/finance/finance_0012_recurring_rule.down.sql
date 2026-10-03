-- +migrate Down
-- finance_0012_recurring_rule: Remove finance_recurring_rules table

DROP INDEX IF EXISTS finance.idx_finance_recurring_next;
DROP INDEX IF EXISTS finance.idx_finance_recurring_family;
DROP TABLE IF EXISTS finance.finance_recurring_rules;
