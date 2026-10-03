package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ddlChangeLog is {code}_change_log transcribed from the published migration
// (server/migrations/finance/finance_0003_change_log_idempotency.up.sql:12-22, identical
// to homeos_0003:12-22) with the six §五 columns and nothing else. There is deliberately
// no `data` and no `created_at`: if the base grows such a field again, the INSERT below
// fails with "no such column" instead of passing against a fixture the migration does not
// describe. (BUS-3's real-database symptom was SQLSTATE 42703 for exactly this.)
const ddlChangeLog = `
CREATE TABLE %s_change_log (
    lsn       INTEGER PRIMARY KEY AUTOINCREMENT,
    family_id TEXT        NOT NULL,
    entity    TEXT        NOT NULL,
    entity_id TEXT        NOT NULL,
    op        TEXT        NOT NULL,
    version   INTEGER     NOT NULL
);
CREATE INDEX %s_change_log_family_lsn_idx ON %s_change_log (family_id, lsn);
`

// ddlIdempotency is {code}_idempotency per the same migration (:33-44): five columns,
// no primary key, and the ONE unique index the migration creates -- (family_id, key).
// Note what this fixture does NOT have: the old test declared `key TEXT PRIMARY KEY` plus
// `request_hash ... UNIQUE`, which is not the published DDL.
const ddlIdempotency = `
CREATE TABLE %s_idempotency (
    key               TEXT        NOT NULL,
    family_id         TEXT        NOT NULL,
    request_hash      TEXT        NOT NULL,
    response_snapshot BLOB,
    created_at        DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX %s_idempotency_family_key_uidx ON %s_idempotency (family_id, key);
`

const (
	testCode     = "test"
	familyUUID   = "11111111-1111-4111-8111-111111111111"
	familyOther  = "22222222-2222-4222-8222-222222222222"
	entityUUID   = "33333333-3333-4333-8333-333333333333"
	entityUUID2  = "44444444-4444-4444-8444-444444444444"
	changeLogDDL = "test_change_log"
)

// setupTestDB builds an in-memory database holding ONLY the two {code}-prefixed tables
// the migration creates, so any write aimed at a bare "change_log"/"idempotency_key"
// table fails loudly instead of silently landing on an AutoMigrated shape.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	stmts := []string{
		fmt.Sprintf(ddlChangeLog, testCode, testCode, testCode),
		fmt.Sprintf(ddlIdempotency, testCode, testCode, testCode),
	}
	// the multi-statement DDL above carries CREATE INDEX lines; split on ';' so sqlite
	// executes each statement separately.
	for _, group := range stmts {
		for _, stmt := range strings.Split(group, ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if err := db.Exec(stmt).Error; err != nil {
				t.Fatalf("fixture DDL rejected (%s): %v", stmt, err)
			}
		}
	}

	// The fixture itself must not contain the columns the base used to write.
	for _, forbidden := range []string{"data", "created_at"} {
		if changeLogColumns(t, db, changeLogDDL)[forbidden] {
			t.Fatalf("fixture bug: %s must not have a %q column", changeLogDDL, forbidden)
		}
	}

	return db
}

func changeLogColumns(t *testing.T, db *gorm.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Raw("SELECT name FROM pragma_table_info(?)", table).Rows()
	if err != nil {
		t.Fatalf("cannot read %s columns: %v", table, err)
	}
	defer rows.Close()

	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		have[name] = true
	}
	return have
}

// TestAppendChangeLogWritesOnlyTheSixDDLColumns is the core falsifiable case: the fixture
// has exactly the migration's columns, so an undeclared column in the base is a hard error.
func TestAppendChangeLogWritesOnlyTheDDLColumns(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()

	tx := db.Begin()
	// the data argument is accepted for call-site compatibility and must NOT be persisted
	if err := repo.AppendChangeLog(ctx, tx, familyUUID, "bill", entityUUID, "CREATE", 3,
		map[string]string{"amount": "12.50", "memo": "不是日志的列"}); err != nil {
		t.Fatalf("AppendChangeLog against the published DDL: %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}

	type row struct {
		LSN      uint64
		FamilyID string
		Entity   string
		EntityID string
		Op       string
		Version  int64
	}
	var got []row
	if err := db.Table(repo.Table()).Order("lsn").Find(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 change log row, got %d", len(got))
	}
	want := row{LSN: 1, FamilyID: familyUUID, Entity: "bill", EntityID: entityUUID, Op: "CREATE", Version: 3}
	if got[0] != want {
		t.Errorf("row = %+v, want %+v", got[0], want)
	}
	if got[0].LSN == 0 {
		t.Error("lsn must come from the sequence, not from Go")
	}
}

// TestAppendChangeLogSQLColumnSet reads the generated INSERT so the claim does not depend
// on a lenient fixture: every column the base writes must be one of §五's six, and the
// NOT NULL ones must actually be sent.
func TestAppendChangeLogSQLColumnSet(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DryRun: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("dry-run open: %v", err)
	}
	repo := NewRepo(db, testCode)

	stmt := db.Session(&gorm.Session{DryRun: true}).
		Table(repo.Table()).
		Create(&changeLog{FamilyID: familyUUID, Entity: "bill", EntityID: entityUUID, Op: "CREATE", Version: 1})
	if stmt.Error != nil {
		t.Fatalf("build INSERT: %v", stmt.Error)
	}
	sql := stmt.Statement.SQL.String()

	if got := insertTarget(t, sql); got != "test_change_log" {
		t.Errorf("INSERT target table = %q, want the code-prefixed %q (%s)", got, "test_change_log", sql)
	}
	if strings.Contains(sql, "data") || strings.Contains(sql, "created_at") {
		t.Errorf("INSERT writes a column the DDL does not have: %s", sql)
	}

	declared := map[string]bool{"lsn": true, "family_id": true, "entity": true, "entity_id": true, "op": true, "version": true}
	inserted := insertColumns(t, sql)
	for _, col := range inserted {
		if !declared[col] {
			t.Errorf("column %q is not in §五's six-column set (declared: %v)", col, sortedKeys(declared))
		}
	}
	for _, required := range []string{"family_id", "entity", "entity_id", "op", "version"} {
		if !contains(inserted, required) {
			t.Errorf("INSERT must write NOT NULL column %q, got %v (%s)", required, inserted, sql)
		}
	}
}

// TestRecordIdempotencySQLColumnSet pins the idempotency INSERT to the five §五 columns.
func TestRecordIdempotencySQLColumnSet(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DryRun: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("dry-run open: %v", err)
	}
	repo := NewRepo(db, testCode)

	stmt := db.Session(&gorm.Session{DryRun: true}).
		Table(repo.IdempotencyTable()).
		Create(&idempotencyKey{Key: "k1", FamilyID: familyUUID, RequestHash: "h", ResponseSnapshot: []byte("{}"), CreatedAt: time.Now().UTC()})
	if stmt.Error != nil {
		t.Fatalf("build INSERT: %v", stmt.Error)
	}
	sql := stmt.Statement.SQL.String()
	if got := insertTarget(t, sql); got != "test_idempotency" {
		t.Errorf("INSERT target table = %q, want the code-prefixed %q (%s)", got, "test_idempotency", sql)
	}

	want := []string{"created_at", "family_id", "key", "request_hash", "response_snapshot"}
	got := insertColumns(t, sql)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("idempotency INSERT columns = %v, want exactly %v (%s)", got, want, sql)
	}
}

// TestIdempotencyUniqueIndexBlocksSameFamilyKey proves the (family_id, key) unique index
// from the migration really rejects a replayed key inside one family.
func TestIdempotencyUniqueIndexBlocksSameFamilyKey(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()
	req := map[string]string{"action": "invite"}

	tx := db.Begin()
	if err := repo.RecordIdempotency(ctx, tx, "key1", familyUUID, req, map[string]string{"result": "ok"}); err != nil {
		t.Fatalf("first RecordIdempotency: %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx = db.Begin()
	err := repo.RecordIdempotency(ctx, tx, "key1", familyUUID, req, map[string]string{"result": "ok"})
	if err == nil {
		_ = tx.Commit().Error
		t.Fatal("duplicate (family_id, key) must be rejected by the unique index, got nil error")
	}
	_ = tx.Rollback()

	// the index that fired must be the composite one, not a key-only or hash-only constraint
	if !strings.Contains(err.Error(), "family_id") || !strings.Contains(err.Error(), "key") {
		t.Errorf("expected the (family_id, key) unique index to fire, got: %v", err)
	}

	var count int64
	if err := db.Table(repo.IdempotencyTable()).Where("key = ? AND family_id = ?", "key1", familyUUID).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected the replay to leave exactly 1 idempotency row, got %d", count)
	}
}

// TestIdempotencySameKeyAllowedAcrossFamilies proves the index is composite -- the
// published DDL has no key-only uniqueness, so one client_request_id reused in another
// family must still be recordable.
func TestIdempotencySameKeyAllowedAcrossFamilies(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()
	req := map[string]string{"action": "invite"}

	for _, family := range []string{familyUUID, familyOther} {
		tx := db.Begin()
		if err := repo.RecordIdempotency(ctx, tx, "shared-key", family, req, map[string]string{"family": family}); err != nil {
			t.Fatalf("RecordIdempotency for family %s: %v", family, err)
		}
		if err := tx.Commit().Error; err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	var count int64
	if err := db.Table(repo.IdempotencyTable()).Where("key = ?", "shared-key").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected one idempotency row per family (2), got %d", count)
	}

	// replay lookup is family-scoped too
	tx := db.Begin()
	found, snapshot, err := repo.CheckIdempotency(ctx, tx, "shared-key", familyOther, req)
	_ = tx.Commit().Error
	if err != nil {
		t.Fatalf("CheckIdempotency: %v", err)
	}
	if !found {
		t.Fatal("expected the other family's row to replay")
	}
	if !strings.Contains(string(snapshot), familyOther) {
		t.Errorf("replayed snapshot = %s, want the %s response", snapshot, familyOther)
	}
}

// TestNoBareTableNameTarget asserts the base never aims at a table the migration does not
// create: only {code}_* tables exist in the fixture, and both write paths must land there.
func TestNoBareTableNameTarget(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()

	tx := db.Begin()
	if err := repo.AppendChangeLog(ctx, tx, familyUUID, "bill", entityUUID, "CREATE", 1, nil); err != nil {
		t.Fatalf("AppendChangeLog: %v", err)
	}
	if err := repo.RecordIdempotency(ctx, tx, "key1", familyUUID, map[string]string{"a": "b"}, map[string]string{"c": "d"}); err != nil {
		t.Fatalf("RecordIdempotency: %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := repo.GetChanges(ctx, DeltaQuery{FamilyID: familyUUID}); err != nil {
		t.Fatalf("GetChanges: %v", err)
	}

	var names []string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'table'").Scan(&names).Error; err != nil {
		t.Fatalf("list tables: %v", err)
	}
	for _, name := range names {
		if name == "change_log" || name == "idempotency_key" || name == "change_logs" || name == "idempotency_keys" {
			t.Errorf("base wrote a table the migration does not create: %q (all tables: %v)", name, names)
		}
		if name != "test_change_log" && name != "test_idempotency" && !strings.HasPrefix(name, "sqlite_") {
			t.Errorf("unexpected table created by the base: %q", name)
		}
	}
}

// TestNewRepoRejectsCodelessTarget: the old bare TableName() defaulted to "change_log",
// a table no real database has. A Repo must refuse instead of pretending the caller will
// remember to pass a code.
func TestNewRepoRejectsCodelessTarget(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	for _, code := range []string{"", "Test", "test space", "test;drop table x", "_test"} {
		repo := NewRepo(db, code)
		if repo.Err() == nil {
			t.Errorf("NewRepo(%q) accepted an invalid service code", code)
			continue
		}
		tx := db.Begin()
		if err := repo.AppendChangeLog(ctx, tx, familyUUID, "bill", entityUUID, "CREATE", 1, nil); !errors.Is(err, repo.Err()) {
			t.Errorf("AppendChangeLog(%q) error = %v, want the construction error %v", code, err, repo.Err())
		}
		if found, _, err := repo.CheckIdempotency(ctx, tx, "k", familyUUID, nil); err == nil || found {
			t.Errorf("CheckIdempotency(%q) must fail closed, got found=%v err=%v", code, found, err)
		}
		if err := repo.RecordIdempotency(ctx, tx, "k", familyUUID, nil, nil); !errors.Is(err, repo.Err()) {
			t.Errorf("RecordIdempotency(%q) error = %v, want the construction error", code, err)
		}
		if _, err := repo.GetChanges(ctx, DeltaQuery{FamilyID: familyUUID}); !errors.Is(err, repo.Err()) {
			t.Errorf("GetChanges(%q) error = %v, want the construction error", code, err)
		}
		_ = tx.Commit().Error

		var count int64
		if err := db.Table("test_change_log").Count(&count).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Errorf("bad code %q still wrote a row", code)
		}
	}
}

// TestAppendChangeLogRequiresTransaction guards the other silent-misroute path: a nil tx.
func TestAppendChangeLogRequiresTransaction(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()

	if err := repo.AppendChangeLog(ctx, nil, familyUUID, "bill", entityUUID, "CREATE", 1, nil); err == nil {
		t.Error("AppendChangeLog(nil tx) must fail, not fall back to the pool")
	}
	if err := repo.RecordIdempotency(ctx, nil, "k", familyUUID, nil, nil); err == nil {
		t.Error("RecordIdempotency(nil tx) must fail")
	}
	if _, _, err := repo.CheckIdempotency(ctx, nil, "k", familyUUID, nil); err == nil {
		t.Error("CheckIdempotency(nil tx) must fail")
	}
}

func TestCheckIdempotencyNotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()

	tx := db.Begin()
	found, snapshot, err := repo.CheckIdempotency(ctx, tx, "nope", familyUUID, map[string]string{"action": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found || len(snapshot) != 0 {
		t.Errorf("found=%v snapshot=%q, want a miss", found, snapshot)
	}
	_ = tx.Commit().Error
}

func TestCheckIdempotencyHashMismatchIsNotAReplay(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()

	tx := db.Begin()
	if err := repo.RecordIdempotency(ctx, tx, "key1", familyUUID, map[string]string{"amount": "1"}, nil); err != nil {
		t.Fatalf("RecordIdempotency: %v", err)
	}
	_ = tx.Commit().Error

	tx = db.Begin()
	found, _, err := repo.CheckIdempotency(ctx, tx, "key1", familyUUID, map[string]string{"amount": "2"})
	_ = tx.Commit().Error
	if err != nil {
		t.Fatalf("CheckIdempotency: %v", err)
	}
	if found {
		t.Error("same client_request_id with a different body must not replay the first response")
	}
}

func TestRecordAndCheckIdempotency(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()

	requestData := map[string]string{"action": "test"}
	responseData := map[string]string{"result": "success"}

	tx := db.Begin()
	if err := repo.RecordIdempotency(ctx, tx, "key1", familyUUID, requestData, responseData); err != nil {
		t.Fatalf("failed to record idempotency: %v", err)
	}
	_ = tx.Commit().Error

	tx = db.Begin()
	found, snapshot, err := repo.CheckIdempotency(ctx, tx, "key1", familyUUID, requestData)
	_ = tx.Commit().Error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Error("expected to find idempotency record")
	}
	if string(snapshot) != `{"result":"success"}` {
		t.Errorf("replayed snapshot = %s, want the first response body", snapshot)
	}
}

func TestGetChanges(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		tx := db.Begin()
		err := repo.AppendChangeLog(ctx, tx, familyUUID, "member", fmt.Sprintf("00000000-0000-4000-8000-00000000000%d", i), "UPDATE", int64(i), nil)
		if err != nil {
			t.Fatalf("failed to insert test data: %v", err)
		}
		_ = tx.Commit().Error
	}

	changes, err := repo.GetChanges(ctx, DeltaQuery{FamilyID: familyUUID, SinceLSN: 2, Limit: 10})
	if err != nil {
		t.Fatalf("failed to get changes: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d", len(changes))
	}
	if changes[0].LSN != 3 || changes[0].Version != 3 || changes[0].Op != "UPDATE" {
		t.Errorf("first entry = %+v, want lsn=3 version=3 op=UPDATE", changes[0])
	}
	// the delta游标 is (family_id, lsn): another family's cursor must see nothing
	other, err := repo.GetChanges(ctx, DeltaQuery{FamilyID: familyOther})
	if err != nil {
		t.Fatalf("GetChanges(other family): %v", err)
	}
	if len(other) != 0 {
		t.Errorf("other family saw %d rows, want 0", len(other))
	}
}

func TestGetChangesDefaultAndMaxLimit(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepo(db, testCode)
	ctx := context.Background()

	for i := 1; i <= 150; i++ {
		tx := db.Begin()
		err := repo.AppendChangeLog(ctx, tx, familyUUID, "member", entityUUID2, "CREATE", int64(i), nil)
		if err != nil {
			t.Fatalf("failed to insert test data #%d: %v", i, err)
		}
		_ = tx.Commit().Error
	}

	changes, err := repo.GetChanges(ctx, DeltaQuery{FamilyID: familyUUID, SinceLSN: 0})
	if err != nil {
		t.Fatalf("failed to get changes: %v", err)
	}
	if len(changes) != 100 {
		t.Errorf("expected 100 changes (default limit), got %d", len(changes))
	}

	capped, err := repo.GetChanges(ctx, DeltaQuery{FamilyID: familyUUID, Limit: 5000})
	if err != nil {
		t.Fatalf("capped query: %v", err)
	}
	if len(capped) != 150 {
		t.Errorf("expected the 150 stored rows under the 1000 cap, got %d", len(capped))
	}
}

func TestComputeRequestHash(t *testing.T) {
	data := map[string]string{"key": "value"}
	hash1 := computeRequestHash(data)
	if hash1 == "" {
		t.Error("expected non-empty hash")
	}
	if computeRequestHash(data) != hash1 {
		t.Error("same data should produce same hash")
	}
	if computeRequestHash(map[string]string{"key": "different"}) == hash1 {
		t.Error("different data should produce different hash")
	}
	if len(hash1) != 64 {
		t.Errorf("hash = %q, want 64 hex chars (sha256)", hash1)
	}
}

func TestDeltaJSONShapeHasNoDataBody(t *testing.T) {
	// §五:283 -- delta 只给变更清单；客户端按 (entity, entity_id) 再取对象。
	entry := DeltaEntry{LSN: 7, Entity: "bill", EntityID: entityUUID, Op: "DELETE", Version: 2}
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "data") {
		t.Errorf("delta entry JSON carries a payload body, which §五:282's column set forbids: %s", body)
	}
	for _, want := range []string{`"lsn":7`, `"entity":"bill"`, `"entity_id":"`, `"op":"DELETE"`, `"version":2`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("delta entry JSON missing %s in %s", want, body)
		}
	}
}

func TestRepoTableNamesAreCodePrefixed(t *testing.T) {
	db := setupTestDB(t)
	for _, code := range []string{"homeos", "finance", testCode} {
		repo := NewRepo(db, code)
		if repo.Err() != nil {
			t.Fatalf("NewRepo(%s): %v", code, repo.Err())
		}
		if got, want := repo.Table(), code+"_change_log"; got != want {
			t.Errorf("Table() = %q, want %q", got, want)
		}
		if got, want := repo.IdempotencyTable(), code+"_idempotency"; got != want {
			t.Errorf("IdempotencyTable() = %q, want %q", got, want)
		}
	}
}

// ==================== helpers ====================

// insertColumns pulls the column list out of a generated INSERT statement.
func insertColumns(t *testing.T, sql string) []string {
	t.Helper()

	match := insertColumnsRe.FindStringSubmatch(sql)
	if match == nil {
		t.Fatalf("cannot find the column list in: %s", sql)
	}
	var cols []string
	for _, raw := range strings.Split(match[1], ",") {
		col := strings.Trim(strings.TrimSpace(raw), "\"`[]")
		if col == "" {
			continue
		}
		cols = append(cols, col)
	}
	if len(cols) == 0 {
		t.Fatalf("empty column list in: %s", sql)
	}
	return cols
}

var insertColumnsRe = regexp.MustCompile(`(?is)INSERT\s+INTO\s+\S+\s*\(([^)]*)\)\s*VALUES`)

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// insertTarget reports the unquoted table an INSERT statement writes.
func insertTarget(t *testing.T, sql string) string {
	t.Helper()

	match := insertTargetRe.FindStringSubmatch(sql)
	if match == nil {
		t.Fatalf("cannot find the INSERT target in: %s", sql)
	}
	return strings.Trim(match[1], "\"`[]")
}

var insertTargetRe = regexp.MustCompile(`(?is)INSERT\s+INTO\s+([^\s(]+)`)
