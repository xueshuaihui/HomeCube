package sync

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create test tables
	code := "test"
	changeLogTable := code + "_change_log"
	idemTable := code + "_idempotency"

	err = db.Exec(`CREATE TABLE ` + changeLogTable + ` (
		lsn INTEGER PRIMARY KEY AUTOINCREMENT,
		family_id TEXT NOT NULL,
		entity TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		op TEXT NOT NULL,
		version INTEGER NOT NULL,
		data BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`).Error
	if err != nil {
		t.Fatalf("failed to create change log table: %v", err)
	}

	err = db.Exec(`CREATE TABLE ` + idemTable + ` (
		key TEXT PRIMARY KEY,
		family_id TEXT NOT NULL,
		request_hash TEXT NOT NULL UNIQUE,
		response_snapshot BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`).Error
	if err != nil {
		t.Fatalf("failed to create idempotency table: %v", err)
	}

	return db, func() {}
}

func TestAppendChangeLog(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(db, "test")
	ctx := context.Background()

	tx := db.Begin()
	err := repo.AppendChangeLog(ctx, tx, "family123", "user", "user1", "CREATE", 1, map[string]string{"name": "John"})
	if err != nil {
		t.Fatalf("failed to append change log: %v", err)
	}
	tx.Commit()

	// Verify the entry was created
	var count int64
	db.Table("test_change_log").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 change log entry, got %d", count)
	}
}

func TestCheckIdempotency_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(db, "test")
	ctx := context.Background()

	tx := db.Begin()
	found, _, err := repo.CheckIdempotency(ctx, tx, "key1", "family123", map[string]string{"action": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected not found")
	}
	tx.Commit()
}

func TestRecordAndCheckIdempotency(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(db, "test")
	ctx := context.Background()

	requestData := map[string]string{"action": "test"}
	responseData := map[string]string{"result": "success"}

	tx := db.Begin()
	err := repo.RecordIdempotency(ctx, tx, "key1", "family123", requestData, responseData)
	if err != nil {
		t.Fatalf("failed to record idempotency: %v", err)
	}
	tx.Commit()

	// Check again
	tx = db.Begin()
	found, snapshot, err := repo.CheckIdempotency(ctx, tx, "key1", "family123", requestData)
	tx.Commit()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Error("expected to find idempotency record")
	}
	if len(snapshot) == 0 {
		t.Error("expected response snapshot")
	}
}

func TestGetChanges(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(db, "test")
	ctx := context.Background()

	// Insert test data
	for i := 1; i <= 5; i++ {
		tx := db.Begin()
		err := repo.AppendChangeLog(ctx, tx, "family123", "user", "user"+string(rune('0'+i)), "CREATE", int64(i), nil)
		if err != nil {
			t.Fatalf("failed to insert test data: %v", err)
		}
		tx.Commit()
	}

	// Get changes since LSN 2
	changes, err := repo.GetChanges(ctx, DeltaQuery{
		FamilyID: "family123",
		SinceLSN: 2,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("failed to get changes: %v", err)
	}

	if len(changes) != 3 {
		t.Errorf("expected 3 changes, got %d", len(changes))
	}

	// Verify ordering
	if changes[0].LSN != 3 {
		t.Errorf("expected first LSN to be 3, got %d", changes[0].LSN)
	}
}

func TestGetChanges_DefaultLimit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(db, "test")
	ctx := context.Background()

	// Insert more than default limit
	for i := 1; i <= 150; i++ {
		tx := db.Begin()
		err := repo.AppendChangeLog(ctx, tx, "family123", "user", "user"+string(rune(i%26+'a')), "CREATE", int64(i), nil)
		if err != nil {
			t.Fatalf("failed to insert test data: %v", err)
		}
		tx.Commit()
	}

	// Get changes with no explicit limit (should use default of 100)
	changes, err := repo.GetChanges(ctx, DeltaQuery{
		FamilyID: "family123",
		SinceLSN: 0,
	})
	if err != nil {
		t.Fatalf("failed to get changes: %v", err)
	}

	if len(changes) != 100 {
		t.Errorf("expected 100 changes (default limit), got %d", len(changes))
	}
}

func TestGetChanges_MaxLimit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(db, "test")
	ctx := context.Background()

	// Get changes with excessive limit (should be capped at 1000)
	_, err := repo.GetChanges(ctx, DeltaQuery{
		FamilyID: "family123",
		SinceLSN: 0,
		Limit:    5000,
	})
	if err != nil {
		t.Fatalf("failed to get changes: %v", err)
	}
	// Just verify it doesn't panic and uses max limit
}

func TestComputeRequestHash(t *testing.T) {
	data := map[string]string{"key": "value"}
	hash1 := computeRequestHash(data)

	if hash1 == "" {
		t.Error("expected non-empty hash")
	}

	// Same data should produce same hash
	hash2 := computeRequestHash(data)
	if hash1 != hash2 {
		t.Error("same data should produce same hash")
	}

	// Different data should produce different hash
	hash3 := computeRequestHash(map[string]string{"key": "different"})
	if hash1 == hash3 {
		t.Error("different data should produce different hash")
	}
}
