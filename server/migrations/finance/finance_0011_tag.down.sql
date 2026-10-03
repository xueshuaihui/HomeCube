-- +migrate Down
-- finance_0011_tag: Remove finance_tags table

DROP INDEX IF EXISTS finance.idx_finance_tag_family;
DROP TABLE IF EXISTS finance.finance_tags;

-- Remove tag_ids column from finance_transaction table
ALTER TABLE finance.finance_transaction DROP COLUMN IF EXISTS tag_ids;
