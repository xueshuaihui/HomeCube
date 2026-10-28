-- +goose Down
-- 回滚 0017：demo 表整支删除（契约原文：M2 收口时必须删除 demo 表及相关接口与事件）。
DROP TABLE IF EXISTS finance.finance_demo_item;
