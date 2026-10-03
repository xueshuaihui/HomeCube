// Package repo provides data access operations for the homeos service.
package repo

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"gorm.io/gorm"
)

var (
	ErrOptimisticLock = errors.New("optimistic lock conflict: record was modified by another request")
)

// generateUUID generates a UUID v4 using crypto/rand.
func generateUUID() string {
	uuid := make([]byte, 16)
	_, err := rand.Read(uuid)
	if err != nil {
		panic(fmt.Sprintf("failed to generate UUID: %v", err))
	}
	// Set version bits (version 4)
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	// Set variant bits (RFC 4122)
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// HomeosRepo provides CRUD operations for homeos entities.
type HomeosRepo struct {
	db *gorm.DB
}

// NewHomeosRepo creates a new homeos repository instance.
func NewHomeosRepo(db *gorm.DB) *HomeosRepo {
	return &HomeosRepo{
		db: db,
	}
}

// ==================== Search Index Operations ====================

// UpsertSearchIndex inserts or updates a search index entry.
// Per PRD 14.5 #7: search index table is maintained by svc-homeos, fed by events from various domains.
func UpsertSearchIndex(ctx context.Context, db *gorm.DB, index *model.HomeosSearchIndex) error {
	if index.ID == "" {
		index.ID = generateUUID()
	}
	now := time.Now()
	index.UpdatedAt = now
	if index.CreatedAt.IsZero() {
		index.CreatedAt = now
	}

	// Use ON CONFLICT for upsert behavior
	err := db.WithContext(ctx).Clauses(
		gorm.OnConflict{
			Columns:   []gorm.Column{{Name: "domain"}, {Name: "entity_id"}},
			UpdateAll: true,
		},
	).Create(index).Error

	if err != nil {
		return fmt.Errorf("failed to upsert search index: %w", err)
	}
	return nil
}

// DeleteSearchIndex marks a search index entry as deleted.
func DeleteSearchIndex(ctx context.Context, db *gorm.DB, domain string, entityID string) error {
	result := db.WithContext(ctx).
		Where("domain = ? AND entity_id = ?", domain, entityID).
		Delete(&model.HomeosSearchIndex{})

	if result.Error != nil {
		return fmt.Errorf("failed to delete search index: %w", result.Error)
	}
	return nil
}

// SearchByKeyword performs a keyword search across all domains for a family.
// Uses pg_trgm extension for fuzzy matching if available.
func SearchByKeyword(ctx context.Context, db *gorm.DB, familyID string, keyword string, limit int) ([]model.HomeosSearchIndex, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100 // Cap at 100 results
	}

	var results []model.HomeosSearchIndex

	// Use ILIKE for basic pattern matching (works without pg_trgm)
	// If pg_trgm is available, this will still work but won't use the GIN index optimally
	keywordPattern := "%" + strings.ToLower(keyword) + "%"

	query := db.WithContext(ctx).
		Where("family_id = ?", familyID).
		Where("LOWER(content) LIKE ?", keywordPattern).
		Order("updated_at DESC").
		Limit(limit).
		Find(&results)

	if query.Error != nil {
		return nil, fmt.Errorf("failed to search: %w", query.Error)
	}

	return results, nil
}
