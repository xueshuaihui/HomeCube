// Package model defines GORM models for the homeos service.
package model

import (
	"time"

	"gorm.io/gorm"
)

// TablePrefix is the schema prefix for homeos tables.
// In production with PostgreSQL, this should be "homeos".
// For testing with SQLite, this can be empty.
var TablePrefix = ""

// setTableName returns the full table name with optional schema prefix.
func setTableName(baseName string) string {
	if TablePrefix != "" {
		return TablePrefix + "." + baseName
	}
	return baseName
}

// HomeosSearchIndex represents the global search index table.
// Per PRD 14.5 #7: "关键词全局搜索 + 只读投影表机制", projection tables exist but search index was missing.
type HomeosSearchIndex struct {
	ID        string         `gorm:"type:uuid;primaryKey" json:"id"`
	FamilyID  string         `gorm:"type:uuid;not null;index:idx_homeos_search_family" json:"family_id"`
	Domain    string         `gorm:"type:varchar(20);not null;index:idx_homeos_search_domain" json:"domain"` // finance/purchase/diet/etc
	Entity    string         `gorm:"type:varchar(50);not null" json:"entity"`                                // transaction/account/category/etc
	EntityID  string         `gorm:"type:uuid;not null" json:"entity_id"`
	Content   string         `gorm:"type:text;not null" json:"content"` // searchable text content (concatenated key fields)
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for HomeosSearchIndex.
func (HomeosSearchIndex) TableName() string {
	return setTableName("homeos_search_index")
}
