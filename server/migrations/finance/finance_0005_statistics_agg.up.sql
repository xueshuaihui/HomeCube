-- finance_0005_statistics_agg.up.sql
-- 创建统计预聚合表，按月桶聚合家庭财务数据

CREATE TABLE IF NOT EXISTS finance_statistics_agg (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id UUID NOT NULL,
    period VARCHAR(7) NOT NULL, -- YYYY-MM format for monthly buckets
    metric_type VARCHAR(20) NOT NULL, -- 'income', 'expense', 'net_balance'
    value BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Add comments
COMMENT ON TABLE finance_statistics_agg IS '财务统计预聚合表，用于加速报表查询';
COMMENT ON COLUMN finance_statistics_agg.family_id IS '家庭ID';
COMMENT ON COLUMN finance_statistics_agg.period IS '统计周期（月桶格式：YYYY-MM）';
COMMENT ON COLUMN finance_statistics_agg.metric_type IS '指标类型：income/expense/net_balance';
COMMENT ON COLUMN finance_statistics_agg.value IS '指标值（分）';

-- Create index for efficient querying by family and period
CREATE INDEX idx_finance_stats_family_period ON finance_statistics_agg (family_id, period);

-- Create unique constraint to prevent duplicate entries
CREATE UNIQUE INDEX idx_finance_stats_unique ON finance_statistics_agg (family_id, period, metric_type);
