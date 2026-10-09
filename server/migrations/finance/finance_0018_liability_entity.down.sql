-- finance 序列 0018：回滚负债对象实体表
-- 仅在迁移失败或需要完全回退时使用

DROP INDEX IF EXISTS finance.idx_finance_liability_deleted_at;
DROP INDEX IF EXISTS finance.idx_finance_liability_kind;
DROP INDEX IF EXISTS finance.idx_finance_liability_status;
DROP INDEX IF EXISTS finance.idx_finance_liability_family_id;
DROP TABLE IF EXISTS finance.finance_liability;
