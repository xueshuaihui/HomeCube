package proj

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupProjTestDB(t *testing.T) (*gorm.DB, func()) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create test projection table
	err = db.Exec(`CREATE TABLE homeos_proj_finance (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		family_id TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		data BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(family_id, entity_id)
	)`).Error
	if err != nil {
		t.Fatalf("failed to create test table: %v", err)
	}

	return db, func() {}
}

func TestUpsert_Insert(t *testing.T) {
	db, cleanup := setupProjTestDB(t)
	defer cleanup()

	writer := NewWriter(db, "homeos")
	ctx := context.Background()

	tx := db.Begin()
	err := writer.Upsert(ctx, tx, "homeos_proj_finance",
		map[string]interface{}{"family_id": "fam123", "entity_id": "ent1"},
		map[string]interface{}{"data": `{"amount": 100}`},
	)
	if err != nil {
		t.Fatalf("failed to upsert: %v", err)
	}
	tx.Commit()

	// Verify record was inserted
	var count int64
	db.Table("homeos_proj_finance").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 record, got %d", count)
	}
}

func TestUpsert_Update(t *testing.T) {
	db, cleanup := setupProjTestDB(t)
	defer cleanup()

	writer := NewWriter(db, "homeos")
	ctx := context.Background()

	// Insert first record
	tx := db.Begin()
	err := writer.Upsert(ctx, tx, "homeos_proj_finance",
		map[string]interface{}{"family_id": "fam123", "entity_id": "ent1"},
		map[string]interface{}{"data": `{"amount": 100}`},
	)
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}
	tx.Commit()

	// Update the same record
	tx = db.Begin()
	err = writer.Upsert(ctx, tx, "homeos_proj_finance",
		map[string]interface{}{"family_id": "fam123", "entity_id": "ent1"},
		map[string]interface{}{"data": `{"amount": 200}`},
	)
	if err != nil {
		t.Fatalf("failed to update: %v", err)
	}
	tx.Commit()

	// Verify only one record exists
	var count int64
	db.Table("homeos_proj_finance").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 record after update, got %d", count)
	}
}

func TestUpsert_InvalidTableName(t *testing.T) {
	db, cleanup := setupProjTestDB(t)
	defer cleanup()

	writer := NewWriter(db, "homeos")
	ctx := context.Background()

	tx := db.Begin()
	err := writer.Upsert(ctx, tx, "invalid_table",
		map[string]interface{}{"family_id": "fam123"},
		map[string]interface{}{"data": "{}"},
	)
	tx.Rollback()

	if err == nil {
		t.Error("expected error for invalid table name")
	}
}

func TestDelete(t *testing.T) {
	db, cleanup := setupProjTestDB(t)
	defer cleanup()

	writer := NewWriter(db, "homeos")
	ctx := context.Background()

	// Insert a record
	tx := db.Begin()
	err := writer.Upsert(ctx, tx, "homeos_proj_finance",
		map[string]interface{}{"family_id": "fam123", "entity_id": "ent1"},
		map[string]interface{}{"data": `{"amount": 100}`},
	)
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}
	tx.Commit()

	// Delete the record
	tx = db.Begin()
	err = writer.Delete(ctx, tx, "homeos_proj_finance",
		map[string]interface{}{"family_id": "fam123", "entity_id": "ent1"},
	)
	if err != nil {
		t.Fatalf("failed to delete: %v", err)
	}
	tx.Commit()

	// Verify record was deleted
	var count int64
	db.Table("homeos_proj_finance").Count(&count)
	if count != 0 {
		t.Errorf("expected 0 records after delete, got %d", count)
	}
}

func TestRebuildScript(t *testing.T) {
	writer := NewWriter(nil, "homeos")

	script := writer.RebuildScript("finance_accounts", "homeos_proj_finance")

	if script == "" {
		t.Error("expected non-empty rebuild script")
	}

	// Verify script contains expected elements
	expectedElements := []string{
		"TRUNCATE TABLE homeos_proj_finance",
		"INSERT INTO homeos_proj_finance",
		"FROM finance_accounts",
		"BEGIN",
		"COMMIT",
	}

	for _, elem := range expectedElements {
		if !contains(script, elem) {
			t.Errorf("rebuild script should contain %q", elem)
		}
	}
}

func TestBatchUpsert(t *testing.T) {
	db, cleanup := setupProjTestDB(t)
	defer cleanup()

	writer := NewWriter(db, "homeos")
	ctx := context.Background()

	records := []map[string]interface{}{
		{"family_id": "fam1", "entity_id": "ent1", "data": `{"val": 1}`},
		{"family_id": "fam1", "entity_id": "ent2", "data": `{"val": 2}`},
		{"family_id": "fam2", "entity_id": "ent3", "data": `{"val": 3}`},
	}

	tx := db.Begin()
	err := writer.BatchUpsert(ctx, tx, "homeos_proj_finance", records)
	if err != nil {
		t.Fatalf("failed to batch upsert: %v", err)
	}
	tx.Commit()

	// Verify all records were inserted
	var count int64
	db.Table("homeos_proj_finance").Count(&count)
	if count != 3 {
		t.Errorf("expected 3 records, got %d", count)
	}
}

func TestBatchUpsert_Empty(t *testing.T) {
	db, cleanup := setupProjTestDB(t)
	defer cleanup()

	writer := NewWriter(db, "homeos")
	ctx := context.Background()

	tx := db.Begin()
	err := writer.BatchUpsert(ctx, tx, "homeos_proj_finance", []map[string]interface{}{})
	tx.Rollback()

	if err != nil {
		t.Errorf("batch upsert with empty records should not error: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
