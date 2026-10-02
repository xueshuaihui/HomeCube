package proj

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Writer provides projection table write capabilities.
// Projection tables follow the naming convention: {code}_proj_{src}
// Only subscribers from the same service can write to these tables.
type Writer struct {
	db   *gorm.DB
	code string
}

// NewWriter creates a new projection writer for the given service code.
func NewWriter(db *gorm.DB, code string) *Writer {
	return &Writer{
		db:   db,
		code: code,
	}
}

// Upsert inserts or updates a projection record.
// tableName should be in format {code}_proj_{src}.
// The operation must be called within a transaction from a subscriber.
func (w *Writer) Upsert(ctx context.Context, tx *gorm.DB, tableName string, key map[string]interface{}, data map[string]interface{}) error {
	if tableName == "" {
		return fmt.Errorf("proj: table name is required")
	}

	// Verify table name follows the expected pattern
	expectedPrefix := w.code + "_proj_"
	if len(tableName) < len(expectedPrefix) || tableName[:len(expectedPrefix)] != expectedPrefix {
		return fmt.Errorf("proj: table name must start with %s", expectedPrefix)
	}

	// Build upsert query
	now := time.Now()
	data["updated_at"] = now

	// Check if record exists
	var count int64
	query := tx.Table(tableName).WithContext(ctx)
	for k, v := range key {
		query = query.Where(k+" = ?", v)
	}
	err := query.Count(&count).Error
	if err != nil {
		return fmt.Errorf("proj: failed to check existing record: %w", err)
	}

	if count > 0 {
		// Update existing
		updateQuery := tx.Table(tableName).WithContext(ctx)
		for k, v := range key {
			updateQuery = updateQuery.Where(k+" = ?", v)
		}
		err = updateQuery.Updates(data).Error
	} else {
		// Insert new
		record := make(map[string]interface{})
		for k, v := range key {
			record[k] = v
		}
		for k, v := range data {
			record[k] = v
		}
		record["created_at"] = now
		err = tx.Table(tableName).WithContext(ctx).Create(record).Error
	}

	if err != nil {
		return fmt.Errorf("proj: failed to upsert record: %w", err)
	}

	return nil
}

// Delete removes a projection record by key.
func (w *Writer) Delete(ctx context.Context, tx *gorm.DB, tableName string, key map[string]interface{}) error {
	if tableName == "" {
		return fmt.Errorf("proj: table name is required")
	}

	query := tx.Table(tableName).WithContext(ctx)
	for k, v := range key {
		query = query.Where(k+" = ?", v)
	}

	err := query.Delete(nil).Error
	if err != nil {
		return fmt.Errorf("proj: failed to delete record: %w", err)
	}

	return nil
}

// RebuildScript generates SQL for full rebuild of a projection table.
// This script can be used to recreate projection tables from source data.
func (w *Writer) RebuildScript(srcTable, projTable string) string {
	return fmt.Sprintf(`-- Full rebuild script for projection table %s
-- Generated at: %s
-- WARNING: This will truncate and rebuild the entire projection table

BEGIN;

-- Truncate existing projection data
TRUNCATE TABLE %s RESTART IDENTITY CASCADE;

-- Rebuild from source
INSERT INTO %s (family_id, entity_id, data, created_at, updated_at)
SELECT 
    s.family_id,
    s.id as entity_id,
    json_build_object(
        'source', '%s',
        'data', row_to_json(s)
    ) as data,
    NOW() as created_at,
    NOW() as updated_at
FROM %s s
ON CONFLICT (family_id, entity_id) DO UPDATE SET
    data = EXCLUDED.data,
    updated_at = NOW();

COMMIT;
`, projTable, time.Now().Format(time.RFC3339), projTable, projTable, srcTable, srcTable)
}

// BatchUpsert performs batch upsert operations for better performance.
func (w *Writer) BatchUpsert(ctx context.Context, tx *gorm.DB, tableName string, records []map[string]interface{}) error {
	if len(records) == 0 {
		return nil
	}

	now := time.Now()
	for _, record := range records {
		record["updated_at"] = now
		if _, ok := record["created_at"]; !ok {
			record["created_at"] = now
		}
	}

	err := tx.Table(tableName).WithContext(ctx).CreateInBatches(records, 100).Error
	if err != nil {
		return fmt.Errorf("proj: failed to batch upsert: %w", err)
	}

	return nil
}
