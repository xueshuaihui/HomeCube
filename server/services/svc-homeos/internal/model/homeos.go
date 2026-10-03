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

// HomeosFamilyModule is the per-family face mount config: the only authoritative source of
// "which faces does this family have enabled" (PRD 3.6 FamilyModule row, 17.8).
// One row per (family, face); "无行即未启用" -- absence means not mounted, not disabled.
// name/icon/visible/born/availability are NOT columns here: they come from registry (16.1)
// and the authz SDK's scope=module check (15.3), and are composed server-side by the handler.
// The json tags below carry the subset of the /family/modules contract block
// (homeos.yaml L296-L319) that this row owns: code, enabled, version.
type HomeosFamilyModule struct {
	ID string `gorm:"type:uuid;primaryKey" json:"id"`
	// Index first column is always family_id (tech plan 2.2). NOT NULL because the
	// "last enabled face" guard (17.8) counts on it.
	FamilyID string `gorm:"type:uuid;not null;uniqueIndex:homeos_family_module_family_code_uidx,priority:1;index:idx_homeos_family_module_family" json:"family_id"`
	// Face code as registered in registry; deliberately not a CHECK/enum in DDL -- adding a
	// face must not require a migration (17.8).
	Code      string         `gorm:"column:code;type:varchar(20);not null;uniqueIndex:homeos_family_module_family_code_uidx,priority:2" json:"code"`
	Enabled   bool           `gorm:"not null;default:false" json:"enabled"`
	EnabledAt *time.Time     `json:"enabled_at"`
	EnabledBy *string        `gorm:"type:uuid" json:"enabled_by,omitempty"`
	Version   int64          `gorm:"not null;default:1" json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-"`
	DeletedBy *string        `gorm:"type:uuid" json:"-"`
}

// TableName returns the table name for HomeosFamilyModule.
func (HomeosFamilyModule) TableName() string {
	return setTableName("homeos_family_module")
}

// HomeosDynamic is one entry of the family dynamics feed (PRD 3.6 Dynamic row, 17.2 zone D).
// Written ONLY by the event bus subscriber (PRD 3.6 "仅由事件总线写入"), which is why the
// read state lives in HomeosDynamicRead rather than on this row.
// json tags mirror home/summary's dynamics.items[] block (homeos.yaml L464-L480):
// id / code / actor_name / action / summary / at.
type HomeosDynamic struct {
	ID       string `gorm:"type:uuid;primaryKey" json:"id"`
	FamilyID string `gorm:"type:uuid;not null;index:homeos_dynamic_family_at_idx,priority:1;index:homeos_dynamic_family_code_at_idx,priority:1" json:"family_id"`
	// Owning face code (PRD 3.6 calls the same concept "source"); drives the face icon and
	// the feed's face filter (17.2, 17.8's five consumers).
	Code          string         `gorm:"column:code;type:varchar(20);not null;index:homeos_dynamic_family_code_at_idx,priority:2" json:"code"`
	ActorMemberID *string        `gorm:"type:uuid" json:"actor_member_id"` // NULL for system-generated entries
	ActorName     string         `gorm:"type:varchar(100);not null" json:"actor_name"`
	Action        string         `gorm:"type:varchar(50);not null" json:"action"`
	Summary       string         `gorm:"type:text;not null" json:"summary"`
	Entity        string         `gorm:"type:varchar(50);not null" json:"entity"`
	EntityID      *string        `gorm:"type:uuid" json:"entity_id"` // NULL for family-level entries
	OnBehalfOf    *string        `gorm:"type:uuid" json:"on_behalf_of,omitempty"`
	At            time.Time      `gorm:"not null;index:homeos_dynamic_family_at_idx,priority:2,sort:desc;index:homeos_dynamic_family_code_at_idx,priority:3,sort:desc" json:"at"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `json:"-"`
}

// TableName returns the table name for HomeosDynamic.
func (HomeosDynamic) TableName() string {
	return setTableName("homeos_dynamic")
}

// HomeosDynamicRead is one member's read receipt for one dynamic entry. Per-member receipts
// rather than a boolean on homeos_dynamic, because (a) PRD 3.6 forbids writers other than the
// bus, and (b) PRD 17.1 #2 keeps the notification read caliber singular -- a shared flag would
// let one member clear another member's unread state.
type HomeosDynamicRead struct {
	ID        string    `gorm:"type:uuid;primaryKey" json:"id"`
	FamilyID  string    `gorm:"type:uuid;not null;index:homeos_dynamic_read_family_member_idx,priority:1" json:"family_id"`
	DynamicID string    `gorm:"type:uuid;not null;uniqueIndex:homeos_dynamic_read_dyn_member_uidx,priority:1" json:"dynamic_id"`
	MemberID  string    `gorm:"type:uuid;not null;uniqueIndex:homeos_dynamic_read_dyn_member_uidx,priority:2;index:homeos_dynamic_read_family_member_idx,priority:2" json:"member_id"`
	ReadAt    time.Time `gorm:"not null" json:"read_at"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName returns the table name for HomeosDynamicRead.
func (HomeosDynamicRead) TableName() string {
	return setTableName("homeos_dynamic_read")
}

// HomeosNotification is one in-app message-center entry (PRD 3.6 Notification row, 17.5,
// tech plan "通知分型与未读"). Rows are fanned out per receiving member at write time, so the
// single unread caliber is read_at IS NULL aggregated by member_id (PRD 17.1 #2).
// json tags mirror the /notifications items block (homeos.yaml L526-L542):
// id / type / content / read_at / created_at.
type HomeosNotification struct {
	ID       string `gorm:"type:uuid;primaryKey" json:"id"`
	FamilyID string `gorm:"type:uuid;not null;index:homeos_notification_member_type_unread_idx,priority:1;index:homeos_notification_member_created_idx,priority:1" json:"family_id"`
	MemberID string `gorm:"type:uuid;not null;index:homeos_notification_member_type_unread_idx,priority:2;index:homeos_notification_member_created_idx,priority:2;uniqueIndex:homeos_notification_member_dedupe_uidx,priority:1" json:"member_id"`
	// Controlled enum, CHECK-constrained in DDL: budget_alert / system / reminder.
	Type    string `gorm:"column:type;type:varchar(20);not null;index:homeos_notification_member_type_unread_idx,priority:3" json:"type"`
	Content string `gorm:"type:text;not null" json:"content"` // never carries L3 content (PRD ch.20)
	// Channel is one of push / inapp / mention; P1 only writes inapp.
	Channel string `gorm:"type:varchar(20);not null;default:inapp" json:"channel"`
	// DedupeKey guards bus replays; unique per member in DDL (NULL keys are unconstrained).
	DedupeKey *string    `gorm:"column:dedupe_key;uniqueIndex:homeos_notification_member_dedupe_uidx,priority:2" json:"dedupe_key,omitempty"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `gorm:"not null;index:homeos_notification_member_created_idx,priority:3,sort:desc" json:"created_at"`
}

// TableName returns the table name for HomeosNotification.
func (HomeosNotification) TableName() string {
	return setTableName("homeos_notification")
}
