-- 反向于 0002：删除三张非分区表。
DROP TABLE IF EXISTS finance.finance_event_dedupe;
DROP TABLE IF EXISTS finance.finance_dead_letter;
DROP TABLE IF EXISTS finance.finance_outbox;
