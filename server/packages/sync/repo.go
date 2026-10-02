package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ChangeLog represents a change log entry in {code}_change_log table.
type ChangeLog struct {
	ID        uint64    `gorm:"column:lsn;primaryKey"` // lsn is bigserial primary key
	FamilyID  string    `gorm:"column:family_id;not null;index:idx_family_lsn"`
	Entity    string    `gorm:"column:entity;not null"`
	EntityID  string    `gorm:"column:entity_id;not null"`
	Op        string    `gorm:"column:op;not null"` // CREATE, UPDATE, DELETE
	Version   int64     `gorm:"column:version;not null"`
	Data      []byte    `gorm:"column:data"`
	CreatedAt time.Time `gorm:"column:created_at;default:CURRENT_TIMESTAMP"`
}

func (ChangeLog) TableName() string {
	return "change_log" // Will be overridden by code-specific tables
}

// IdempotencyKey represents an idempotency record in {code}_idempotency table.
type IdempotencyKey struct {
	Key              string    `gorm:"column:key;primaryKey"`
	FamilyID         string    `gorm:"column:family_id;not null"`
	RequestHash      string    `gorm:"column:request_hash;not null;uniqueIndex:uk_request_hash"`
	ResponseSnapshot []byte    `gorm:"column:response_snapshot"`
	CreatedAt        time.Time `gorm:"column:created_at;default:CURRENT_TIMESTAMP"`
}

func (IdempotencyKey) TableName() string {
	return "idempotency_key" // Will be overridden by code-specific tables
}

// DeltaQuery represents parameters for querying changes.
type DeltaQuery struct {
	FamilyID string
	SinceLSN uint64
	Limit    int
}

// DeltaEntry represents a single change delta entry.
type DeltaEntry struct {
	LSN      uint64          `json:"lsn"`
	Entity   string          `json:"entity"`
	EntityID string          `json:"entity_id"`
	Op       string          `json:"op"`
	Version  int64           `json:"version"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Repo is the base class for sync repository operations.
// Business write operations append to {code}_change_log within transactions.
type Repo struct {
	db        *gorm.DB
	code      string
	tableName string
	idemTable string
}

// NewRepo creates a new sync repository instance.
// code is the service code (e.g., "homeos", "finance").
func NewRepo(db *gorm.DB, code string) *Repo {
	return &Repo{
		db:        db,
		code:      code,
		tableName: fmt.Sprintf("%s_change_log", code),
		idemTable: fmt.Sprintf("%s_idempotency", code),
	}
}

// AppendChangeLog appends a change log entry within a transaction.
// This should be called from business write operations.
func (r *Repo) AppendChangeLog(ctx context.Context, tx *gorm.DB, familyID, entity, entityID, op string, version int64, data interface{}) error {
	var dataBytes []byte
	if data != nil {
		var err error
		dataBytes, err = json.Marshal(data)
		if err != nil {
			return fmt.Errorf("sync: failed to marshal change data: %w", err)
		}
	}

	changeLog := ChangeLog{
		FamilyID:  familyID,
		Entity:    entity,
		EntityID:  entityID,
		Op:        op,
		Version:   version,
		Data:      dataBytes,
		CreatedAt: time.Now(),
	}

	// Use the code-specific table name
	result := tx.Table(r.tableName).WithContext(ctx).Create(&changeLog)
	return result.Error
}

// CheckIdempotency checks if a request has already been processed.
// Returns true and the cached response if found.
func (r *Repo) CheckIdempotency(ctx context.Context, tx *gorm.DB, key, familyID string, requestData interface{}) (bool, []byte, error) {
	hash := computeRequestHash(requestData)

	var idem IdempotencyKey
	result := tx.Table(r.idemTable).WithContext(ctx).
		Where("key = ? AND family_id = ?", key, familyID).
		First(&idem)

	if result.Error == gorm.ErrRecordNotFound {
		return false, nil, nil
	}
	if result.Error != nil {
		return false, nil, fmt.Errorf("sync: failed to check idempotency: %w", result.Error)
	}

	// Verify hash matches
	if idem.RequestHash != hash {
		return false, nil, nil
	}

	return true, idem.ResponseSnapshot, nil
}

// RecordIdempotency records a successful request for idempotency checking.
func (r *Repo) RecordIdempotency(ctx context.Context, tx *gorm.DB, key, familyID string, requestData interface{}, response interface{}) error {
	hash := computeRequestHash(requestData)

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("sync: failed to marshal response: %w", err)
	}

	idem := IdempotencyKey{
		Key:              key,
		FamilyID:         familyID,
		RequestHash:      hash,
		ResponseSnapshot: responseBytes,
		CreatedAt:        time.Now(),
	}

	result := tx.Table(r.idemTable).WithContext(ctx).Create(&idem)
	return result.Error
}

// GetChanges returns delta changes since the given LSN.
// Implements GET /api/{code}/sync/changes?family_id&since_lsn&limit
func (r *Repo) GetChanges(ctx context.Context, query DeltaQuery) ([]DeltaEntry, error) {
	if query.Limit <= 0 {
		query.Limit = 100 // Default limit
	}
	if query.Limit > 1000 {
		query.Limit = 1000 // Max limit
	}

	var logs []ChangeLog
	result := r.db.WithContext(ctx).Table(r.tableName).
		Where("family_id = ? AND lsn > ?", query.FamilyID, query.SinceLSN).
		Order("lsn ASC").
		Limit(query.Limit).
		Find(&logs)

	if result.Error != nil {
		return nil, fmt.Errorf("sync: failed to get changes: %w", result.Error)
	}

	entries := make([]DeltaEntry, 0, len(logs))
	for _, log := range logs {
		entry := DeltaEntry{
			LSN:      log.ID,
			Entity:   log.Entity,
			EntityID: log.EntityID,
			Op:       log.Op,
			Version:  log.Version,
		}
		if len(log.Data) > 0 {
			entry.Data = log.Data
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// computeRequestHash computes a SHA256 hash of the request data.
func computeRequestHash(data interface{}) string {
	bytes, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:])
}
