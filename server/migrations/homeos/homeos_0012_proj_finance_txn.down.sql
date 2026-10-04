-- 反向于 0012。这张表是 0004 月桶的交易级重算源，同属「可随时全量重建」的本地投影副本（§六），
-- 删除它不销毁任何唯一数据源——finance.transaction.* 事件流可把月桶整段重放重建。
DROP TABLE IF EXISTS homeos.homeos_proj_finance_txn;
