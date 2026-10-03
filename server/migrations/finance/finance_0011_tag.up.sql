-- +migrate Up
-- finance_0011_tag: Add finance_tags table for transaction tagging (PRD 4.4, 18.3#7)

CREATE TABLE finance.finance_tags (
    id UUID PRIMARY KEY,
    family_id UUID NOT NULL,
    name VARCHAR(50) NOT NULL,
    color VARCHAR(7),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP,
    deleted_by UUID
);

CREATE INDEX idx_finance_tag_family ON finance.finance_tags(family_id);

-- Add tag_ids column to finance_transaction table
ALTER TABLE finance.finance_transaction ADD COLUMN IF NOT EXISTS tag_ids JSONB;
