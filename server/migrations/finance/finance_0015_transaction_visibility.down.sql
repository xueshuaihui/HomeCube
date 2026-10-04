-- +goose Down
-- 回滚 PRD 15.4 的两列。注意 CHECK 约束必须先删，否则 DROP COLUMN 会连带失败。
--
-- 这里只回滚结构，**不还原 visibility 的值**：列被删掉后 visibility 语义即丢失，
-- 若再次 up 会全部回到 DEFAULT 'shared'（等价于「全部可见」）。因此 down 之后
-- P1 处于「L3 过滤不可用」的状态，不应把 down 后的库当作可交付环境。
ALTER TABLE finance.finance_transaction
    DROP CONSTRAINT IF EXISTS ck_finance_transaction_visibility;

DROP INDEX IF EXISTS finance.idx_finance_transaction_created_by;

ALTER TABLE finance.finance_transaction
    DROP COLUMN IF EXISTS visibility;

ALTER TABLE finance.finance_transaction
    DROP COLUMN IF EXISTS created_by;
