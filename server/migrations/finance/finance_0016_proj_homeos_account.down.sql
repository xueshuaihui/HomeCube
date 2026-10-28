-- +goose Down
-- 回滚 0016：先删依赖 account_id 的部分唯一索引，再删列。
DROP INDEX IF EXISTS finance.finance_proj_homeos_family_account_uidx;
ALTER TABLE finance.finance_proj_homeos DROP COLUMN IF EXISTS account_id;
