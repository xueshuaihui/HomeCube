-- +goose Down
DROP INDEX IF EXISTS finance.idx_finance_settings_family_id;
DROP TABLE IF EXISTS finance.finance_settings;
