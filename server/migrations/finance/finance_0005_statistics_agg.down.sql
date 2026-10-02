-- finance_0005_statistics_agg.down.sql
-- 删除统计预聚合表

DROP INDEX IF EXISTS idx_finance_stats_unique;
DROP INDEX IF EXISTS idx_finance_stats_family_period;
DROP TABLE IF EXISTS finance_statistics_agg;
