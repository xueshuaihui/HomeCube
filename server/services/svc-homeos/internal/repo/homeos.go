// Package repo provides data access operations for the homeos service.
package repo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrOptimisticLock = errors.New("optimistic lock conflict: record was modified by another request")
	// ErrModuleVersionConflict is the distinguishable "PUT family/modules sent a stale version"
	// signal (contract homeos.yaml L361 -> HTTP 409). It wraps ErrOptimisticLock so callers can
	// match either the specific or the generic error.
	ErrModuleVersionConflict = fmt.Errorf("%w: family module version mismatch", ErrOptimisticLock)
	// ErrModuleNotFound means the family has no row for that face code, i.e. 未启用 (PRD 17.8
	// 「无行即未启用」). Not a 404 candidate on its own: absence is a legal state.
	ErrModuleNotFound = errors.New("family module row not found (face not mounted for this family)")
	// ErrLastEnabledModule enforces PRD 17.8 停用语义第 4 条「不允许出现 0 面家庭」
	// (contract homeos.yaml L362 -> HTTP 409 "版本冲突或仅剩最后一面不允许停用").
	ErrLastEnabledModule = errors.New("cannot disable the last enabled family module")
	// ErrDynamicNotFound is returned when a mark-read targets a dynamic outside the family.
	ErrDynamicNotFound = errors.New("dynamic entry not found in this family")
	// ErrInvalidCursor is returned for a malformed page cursor; handlers map it to 400 and
	// must NOT fall back to the first page (silently repeating page 1 looks like a data bug).
	ErrInvalidCursor = errors.New("invalid pagination cursor")
	// ErrInvalidArgument covers bad enum values / missing ids, which handlers map to 400.
	ErrInvalidArgument = errors.New("invalid argument")
)

// NotificationTypes is the controlled enum of homeos_notification.type -- PRD 3.6 and tech plan
// 「通知分型与未读」: "新增取值须同时改事件目录与客户端文案库". Kept here so a bad ?type= filter
// is rejected instead of silently returning an empty page.
var NotificationTypes = []string{"budget_alert", "system", "reminder"}

// ValidNotificationType reports whether t is one of the three contract values.
func ValidNotificationType(t string) bool {
	for _, v := range NotificationTypes {
		if v == t {
			return true
		}
	}
	return false
}

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
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "domain"}, {Name: "entity_id"}},
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

// ==================== Cursor Pagination ====================
//
// svc-homeos has no pre-existing cursor encoding (the search index paths are limit-only), so
// this is the convention for this service, per PRD 3.7 "游标分页" and 4.5 "流水筛选与游标分页"
// (offset paging is not used anywhere in this codebase):
//
//	a cursor is the opaque base64url of "<at RFC3339Nano>~<id>" -- the (timestamp, id) key of
//	the LAST row of the page just returned. Rows are ordered by (<time col> DESC, id DESC), so
//	the next page is the set strictly below that pair; the id tiebreaker makes the boundary
//	total even for rows sharing one timestamp (batch-generated events).
//
// Encoding is not a secret and not stable across releases: it is opaque to the client only so
// that it cannot build WHERE clauses itself.

const cursorSeparator = "~"

// encodeCursor turns the last row's (at, id) into the next_cursor string.
func encodeCursor(at time.Time, id string) string {
	raw := at.UTC().Format(time.RFC3339Nano) + cursorSeparator + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a cursor back into (at, id). An empty cursor is not an error here: callers
// treat "" as "first page" before reaching this function.
func decodeCursor(raw string) (time.Time, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	text := string(decoded)
	// UUIDs never contain the separator, but LastIndex keeps the timestamp side parseable.
	sep := strings.LastIndex(text, cursorSeparator)
	if sep <= 0 || sep == len(text)-1 {
		return time.Time{}, "", fmt.Errorf("%w: malformed cursor payload", ErrInvalidCursor)
	}
	at, err := time.Parse(time.RFC3339Nano, text[:sep])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	return at.UTC(), text[sep+1:], nil
}

// applyTimeIDCursor restricts a query to rows strictly below (at, id) on the given time column,
// matching the (time DESC, id DESC) ordering. Written as an expanded comparison rather than a
// row-value expression so the same statement runs on PostgreSQL and on the SQLite test DB.
func applyTimeIDCursor(query *gorm.DB, timeColumn string, at time.Time, id string) *gorm.DB {
	return query.Where(
		"("+timeColumn+" < ? OR ("+timeColumn+" = ? AND id < ?))",
		at, at, id,
	)
}

// clampLimit applies this service's page-size policy: default 20 (17.2 D 区前 20 条),
// capped at 100 as in SearchByKeyword.
func clampLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

// isUniqueViolation reports a unique-index collision in an engine-neutral way. The repo runs on
// PostgreSQL in production and on SQLite in tests, and neither driver's error type is imported
// here on purpose (pgconn would become a direct dependency of the service for one check).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") || // postgres: duplicate key value violates unique constraint
		strings.Contains(msg, "unique constraint") // sqlite:  UNIQUE constraint failed
}

// emptyAsNull turns a zero uuid into NULL. Binding "" against a `uuid` column is an input-syntax
// error on PostgreSQL, and the audit columns here are legitimately empty (a change with no
// acting member) rather than an unknown value.
func emptyAsNull(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// ==================== Family Module Config (PRD 17.8, /family/modules) ====================

// ListFamilyModules returns every module row of a family, soft-deleted rows excluded by GORM's
// default scope. "无行即未启用": a face missing from the result is not mounted -- the caller
// merges this with the registry face catalog and the authz scope=module verdict.
func ListFamilyModules(ctx context.Context, db *gorm.DB, familyID string) ([]model.HomeosFamilyModule, error) {
	rows := make([]model.HomeosFamilyModule, 0, len(NotificationTypes))
	if familyID == "" {
		return nil, fmt.Errorf("%w: family_id is required", ErrInvalidArgument)
	}
	if err := db.WithContext(ctx).
		Where("family_id = ?", familyID).
		Order("code ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list family modules: %w", err)
	}
	return rows, nil
}

// GetFamilyModule returns the family's row for one face code, or (nil, nil) when the family has
// not mounted that face (PRD 17.8 「无行即未启用」 is a legal state, not a lookup failure).
func GetFamilyModule(ctx context.Context, db *gorm.DB, familyID, code string) (*model.HomeosFamilyModule, error) {
	if familyID == "" || code == "" {
		return nil, fmt.Errorf("%w: family_id and code are required", ErrInvalidArgument)
	}
	var row model.HomeosFamilyModule
	err := db.WithContext(ctx).Where("family_id = ? AND code = ?", familyID, code).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get family module: %w", err)
	}
	return &row, nil
}

// CountEnabledFamilyModules counts a family's currently enabled faces. Two callers need it:
// the 首页 C 区标题「已启用 N 面」(17.2) and the 17.8 「仅剩最后一个已启用面时停用被拒」 guard,
// which SetFamilyModuleEnabled also runs inside its own transaction.
func CountEnabledFamilyModules(ctx context.Context, db *gorm.DB, familyID string) (int64, error) {
	if familyID == "" {
		return 0, fmt.Errorf("%w: family_id is required", ErrInvalidArgument)
	}
	var count int64
	if err := db.WithContext(ctx).Model(&model.HomeosFamilyModule{}).
		Where("family_id = ? AND enabled = ?", familyID, true).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count enabled family modules: %w", err)
	}
	return count, nil
}

// SetFamilyModuleEnabled switches one face's enabled flag for a family under the version
// optimistic lock (PRD 17.8 「写 FamilyModule（version 乐观锁）」, contract PUT /family/modules
// requires {code, enabled, version} and defines 409 for both the version mismatch and the
// last-face case).
//
// expectedVersion is the version the caller last read; 0 means "I expect no row yet" and is the
// only way to create the row (the 强制选面 path of 17.8: a new family must land >=1 rows).
// operatorMemberID is recorded as enabled_by.
//
// Errors are distinguishable on purpose: ErrModuleVersionConflict / ErrOptimisticLock for a
// stale write, ErrLastEnabledModule for the disable-floor, ErrModuleNotFound for "no row and the
// caller did not claim version 0". Disabling keeps the row (17.8 「停用不删数据」).
func SetFamilyModuleEnabled(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	code string,
	enabled bool,
	expectedVersion int64,
	operatorMemberID string,
) (*model.HomeosFamilyModule, error) {
	if familyID == "" || code == "" {
		return nil, fmt.Errorf("%w: family_id and code are required", ErrInvalidArgument)
	}
	if expectedVersion < 0 {
		return nil, fmt.Errorf("%w: version must not be negative", ErrInvalidArgument)
	}

	var result model.HomeosFamilyModule
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.HomeosFamilyModule
		lookupErr := tx.Where("family_id = ? AND code = ?", familyID, code).First(&current).Error

		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			if expectedVersion != 0 {
				return ErrModuleNotFound
			}
			now := time.Now().UTC()
			fresh := model.HomeosFamilyModule{
				ID:        generateUUID(),
				FamilyID:  familyID,
				Code:      code,
				Enabled:   enabled,
				Version:   1,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if enabled {
				fresh.EnabledAt = &now
				fresh.EnabledBy = emptyAsNull(operatorMemberID)
			}
			if err := tx.Create(&fresh).Error; err != nil {
				// Lost the race against a concurrent enable of the same (family, code):
				// the partial unique index homeos_family_module_family_code_uidx rejects us.
				if isUniqueViolation(err) {
					return ErrModuleVersionConflict
				}
				return fmt.Errorf("failed to create family module: %w", err)
			}
			result = fresh
			return nil
		}
		if lookupErr != nil {
			return fmt.Errorf("failed to load family module: %w", lookupErr)
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("%w: family %q face %q is at version %d, request carried %d",
				ErrModuleVersionConflict, familyID, code, current.Version, expectedVersion)
		}

		// Switching to the state it is already in is a no-op that still costs the caller a
		// version bump on retry; return the row untouched so the client can resync.
		if current.Enabled == enabled {
			result = current
			return nil
		}

		if !enabled {
			// PRD 17.8 第 4 条：不允许出现 0 面家庭. Counted inside this transaction so two
			// concurrent disables of the last two faces cannot both pass.
			var enabledCount int64
			if err := tx.Model(&model.HomeosFamilyModule{}).
				Where("family_id = ? AND enabled = ?", familyID, true).
				Count(&enabledCount).Error; err != nil {
				return fmt.Errorf("failed to count enabled family modules: %w", err)
			}
			if enabledCount <= 1 {
				return fmt.Errorf("%w: family %q only has face %q left", ErrLastEnabledModule, familyID, code)
			}
		}

		now := time.Now().UTC()
		updates := map[string]interface{}{
			"enabled":    enabled,
			"version":    gorm.Expr("version + 1"),
			"updated_at": now,
		}
		if enabled {
			updates["enabled_at"] = now
			updates["enabled_by"] = emptyAsNull(operatorMemberID)
		}
		// The version predicate is the lock itself: if someone committed between our read and
		// this write, zero rows match and we report the conflict instead of clobbering them.
		write := tx.Model(&model.HomeosFamilyModule{}).
			Where("id = ? AND version = ?", current.ID, current.Version).
			Updates(updates)
		if write.Error != nil {
			return fmt.Errorf("failed to update family module: %w", write.Error)
		}
		if write.RowsAffected == 0 {
			return ErrModuleVersionConflict
		}
		return tx.Where("id = ?", current.ID).First(&result).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ==================== Dynamics Feed (PRD 3.6 Dynamic, 17.2 D 区, GET /dynamics) ====================

// AppendDynamic writes one feed entry. PRD 3.6 states dynamics are "仅由事件总线写入", so this
// function exists to be called from a subscriber (packages/bus consumer), never from a request
// handler. Read state is not written here -- see MarkDynamicRead on homeos_dynamic_read.
func AppendDynamic(ctx context.Context, db *gorm.DB, dynamic *model.HomeosDynamic) error {
	if dynamic == nil {
		return fmt.Errorf("%w: dynamic is required", ErrInvalidArgument)
	}
	if dynamic.FamilyID == "" || dynamic.Code == "" {
		return fmt.Errorf("%w: family_id and code are required", ErrInvalidArgument)
	}
	if dynamic.ID == "" {
		dynamic.ID = generateUUID()
	}
	if dynamic.At.IsZero() {
		dynamic.At = time.Now().UTC()
	}
	now := time.Now().UTC()
	dynamic.CreatedAt = now
	dynamic.UpdatedAt = now
	if err := db.WithContext(ctx).Create(dynamic).Error; err != nil {
		return fmt.Errorf("failed to append dynamic: %w", err)
	}
	return nil
}

// unreadDynamicsCondition returns the query fragment "this member has no read receipt for that
// row". It is expressed as NOT IN over the receipt table instead of a correlated NOT EXISTS so
// the statement never has to qualify homeos_dynamic by (possibly schema-prefixed) table name --
// which keeps it valid on PostgreSQL and SQLite alike. dynamic_id is NOT NULL, so NOT IN cannot
// be tripped by the NULL-empty-set pitfall.
func unreadDynamicsCondition(db *gorm.DB, memberID string) *gorm.DB {
	receipts := db.Session(&gorm.Session{NewDB: true}).
		Table((&model.HomeosDynamicRead{}).TableName()).
		Select("dynamic_id").
		Where("member_id = ?", memberID)
	return db.Where("id NOT IN (?)", receipts)
}

// ListDynamics returns one page of the family dynamics feed, newest first.
//
//   - code: non-nil and non-empty filters to one face (动态流的面筛选, one of 17.8's five
//     consumers of the face set). nil/"" = all faces.
//   - onlyUnread: drop rows this member already has a receipt for (dynamic page's "只看未读").
//     It requires memberID; an empty memberID is rejected rather than degraded, because
//     silently ignoring the filter would show read entries as unread.
//   - cursor: encodeCursor output of the previous page's last (at, id); "" = first page.
//     A malformed cursor returns ErrInvalidCursor.
//
// The returned *string is next_cursor, nil when the feed is exhausted. home/summary's D 区 calls
// this with limit 20 (17.2 "前 20 条").
func ListDynamics(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	memberID string,
	code *string,
	onlyUnread bool,
	cursor *string,
	limit int,
) ([]model.HomeosDynamic, *string, error) {
	if familyID == "" {
		return nil, nil, fmt.Errorf("%w: family_id is required", ErrInvalidArgument)
	}
	if onlyUnread && memberID == "" {
		return nil, nil, fmt.Errorf("%w: member_id is required for the unread filter", ErrInvalidArgument)
	}
	limit = clampLimit(limit)

	query := db.WithContext(ctx).Where("family_id = ?", familyID)
	if code != nil && *code != "" {
		query = query.Where("code = ?", *code)
	}
	if onlyUnread {
		query = unreadDynamicsCondition(query, memberID)
	}
	if cursor != nil && *cursor != "" {
		at, lastID, err := decodeCursor(*cursor)
		if err != nil {
			return nil, nil, err
		}
		query = applyTimeIDCursor(query, "at", at, lastID)
	}

	var rows []model.HomeosDynamic
	// One extra row probed for "is there a next page" so we never hand back a bogus cursor.
	if err := query.Order("at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list dynamics: %w", err)
	}

	var nextCursor *string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		encoded := encodeCursor(last.At, last.ID)
		nextCursor = &encoded
	}
	return rows, nextCursor, nil
}

// MarkDynamicRead records one member's read receipt for one dynamic entry. Idempotent: a second
// call for the same pair is a no-op (unique index + ON CONFLICT DO NOTHING).
func MarkDynamicRead(ctx context.Context, db *gorm.DB, familyID, memberID, dynamicID string) error {
	if familyID == "" || memberID == "" || dynamicID == "" {
		return fmt.Errorf("%w: family_id, member_id and dynamic_id are required", ErrInvalidArgument)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Scope the existence check by family: a member must not be able to mark another
		// family's entry read (15.2 「判定始终以 family_id 为界」).
		var count int64
		if err := tx.Model(&model.HomeosDynamic{}).
			Where("id = ? AND family_id = ?", dynamicID, familyID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed to check dynamic: %w", err)
		}
		if count == 0 {
			return ErrDynamicNotFound
		}
		now := time.Now().UTC()
		receipt := model.HomeosDynamicRead{
			ID:        generateUUID(),
			FamilyID:  familyID,
			DynamicID: dynamicID,
			MemberID:  memberID,
			ReadAt:    now,
			CreatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "dynamic_id"}, {Name: "member_id"}},
			DoNothing: true,
		}).Create(&receipt).Error; err != nil {
			return fmt.Errorf("failed to mark dynamic read: %w", err)
		}
		return nil
	})
}

// MarkAllDynamicsRead writes receipts for every still-unread entry of a family for this member
// and returns how many entries were newly marked (0 means everything already was read).
// code narrows it to one face (nil/"" = all faces) so the dynamic page's per-face "全部已读"
// clears exactly what it rendered.
func MarkAllDynamicsRead(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	memberID string,
	code *string,
) (int64, error) {
	if familyID == "" || memberID == "" {
		return 0, fmt.Errorf("%w: family_id and member_id are required", ErrInvalidArgument)
	}

	var pending []string
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&model.HomeosDynamic{}).
			Where("family_id = ?", familyID)
		if code != nil && *code != "" {
			query = query.Where("code = ?", *code)
		}
		query = unreadDynamicsCondition(query, memberID)
		if err := query.Order("at DESC").Pluck("id", &pending).Error; err != nil {
			return fmt.Errorf("failed to select unread dynamics: %w", err)
		}
		if len(pending) == 0 {
			return nil
		}
		now := time.Now().UTC()
		receipts := make([]model.HomeosDynamicRead, 0, len(pending))
		for _, id := range pending {
			receipts = append(receipts, model.HomeosDynamicRead{
				ID:        generateUUID(),
				FamilyID:  familyID,
				DynamicID: id,
				MemberID:  memberID,
				ReadAt:    now,
				CreatedAt: now,
			})
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "dynamic_id"}, {Name: "member_id"}},
			DoNothing: true,
		}).CreateInBatches(&receipts, 200).Error; err != nil {
			return fmt.Errorf("failed to write read receipts: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int64(len(pending)), nil
}

// CountUnreadDynamics returns the family's entries this member has no receipt for. code narrows
// it to one face (nil/"" = all faces).
func CountUnreadDynamics(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	memberID string,
	code *string,
) (int64, error) {
	if familyID == "" || memberID == "" {
		return 0, fmt.Errorf("%w: family_id and member_id are required", ErrInvalidArgument)
	}
	var count int64
	query := db.WithContext(ctx).Model(&model.HomeosDynamic{}).
		Where("family_id = ?", familyID)
	if code != nil && *code != "" {
		query = query.Where("code = ?", *code)
	}
	query = unreadDynamicsCondition(query, memberID)
	if err := query.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count unread dynamics: %w", err)
	}
	return count, nil
}

// ==================== Message Center (PRD 3.6 Notification, 17.1, /notifications) ====================

// ListNotifications returns one page of a member's message-center entries, newest first,
// optionally filtered by type (one of NotificationTypes; any other value is ErrInvalidArgument,
// not an empty page -- the client renders three type tabs off this filter).
//
// The unread caliber is read_at IS NULL aggregated by member_id (PRD 17.1 #2) and this is one of
// its two exits, the other being home/summary's unread via CountUnreadNotifications.
func ListNotifications(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	memberID string,
	typ *string,
	cursor *string,
	limit int,
) ([]model.HomeosNotification, *string, error) {
	if familyID == "" || memberID == "" {
		return nil, nil, fmt.Errorf("%w: family_id and member_id are required", ErrInvalidArgument)
	}
	if typ != nil && *typ != "" && !ValidNotificationType(*typ) {
		return nil, nil, fmt.Errorf("%w: unknown notification type %q", ErrInvalidArgument, *typ)
	}
	limit = clampLimit(limit)

	query := db.WithContext(ctx).Where("family_id = ? AND member_id = ?", familyID, memberID)
	if typ != nil && *typ != "" {
		query = query.Where("type = ?", *typ)
	}
	if cursor != nil && *cursor != "" {
		at, lastID, err := decodeCursor(*cursor)
		if err != nil {
			return nil, nil, err
		}
		query = applyTimeIDCursor(query, "created_at", at, lastID)
	}

	var rows []model.HomeosNotification
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list notifications: %w", err)
	}

	var nextCursor *string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		encoded := encodeCursor(last.CreatedAt, last.ID)
		nextCursor = &encoded
	}
	return rows, nextCursor, nil
}

// MarkNotificationsRead sets read_at on this member's unread entries and returns marked_count
// (contract POST /notifications/read -> {marked_count, unread}). Empty ids means "全部已读".
// Foreign or already-read ids fall out of the predicate, so the count is the rows this call
// actually flipped -- that is what both red-dot positions get cleared by (17.1 #4).
func MarkNotificationsRead(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	memberID string,
	ids []string,
) (int64, error) {
	if familyID == "" || memberID == "" {
		return 0, fmt.Errorf("%w: family_id and member_id are required", ErrInvalidArgument)
	}
	query := db.WithContext(ctx).Model(&model.HomeosNotification{}).
		Where("family_id = ? AND member_id = ? AND read_at IS NULL", familyID, memberID)
	if len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	}
	now := time.Now().UTC()
	write := query.Update("read_at", now)
	if write.Error != nil {
		return 0, fmt.Errorf("failed to mark notifications read: %w", write.Error)
	}
	return write.RowsAffected, nil
}

// unreadTypeRow is the GROUP BY shape of the per-type unread query.
type unreadTypeRow struct {
	Type  string `gorm:"column:type"`
	Count int64  `gorm:"column:count"`
}

// UnreadNotificationCounts returns the per-type unread counts and the total for one member --
// the three type tabs of the message page (17.1 #3) and the single `unread` number that
// home/summary and /notifications must agree on value for after 全部已读.
//
// Only types with at least one unread entry appear in the map; callers render the missing three
// as 0. Total is the sum over the same rows, so the two can never disagree.
func UnreadNotificationCounts(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	memberID string,
) (map[string]int64, int64, error) {
	if familyID == "" || memberID == "" {
		return nil, 0, fmt.Errorf("%w: family_id and member_id are required", ErrInvalidArgument)
	}
	var rows []unreadTypeRow
	if err := db.WithContext(ctx).
		Model(&model.HomeosNotification{}).
		Select("type, COUNT(*) AS count").
		Where("family_id = ? AND member_id = ? AND read_at IS NULL", familyID, memberID).
		Group("type").
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count unread notifications: %w", err)
	}
	byType := make(map[string]int64, len(NotificationTypes))
	var total int64
	for _, r := range rows {
		byType[r.Type] = r.Count
		total += r.Count
	}
	return byType, total, nil
}

// ==================== Home Summary B 区 (17.2 今日区) ====================

// DueWindow is the two time ranges home/summary's B 区 needs, already converted to instants by
// the caller: the day whose due items are rendered is [DayStart, DayEnd) in the FAMILY timezone
// (17.2 「时段按家庭时区」), and [PeriodFrom, PeriodTo) is the shell-level period selection
// (月/季/年三档, 定版⑬) that the finance projection is summed over.
type DueWindow struct {
	DayStart   time.Time
	DayEnd     time.Time
	PeriodFrom time.Time
	PeriodTo   time.Time
}

// DueTodayItem is one B 区 small card (contract home/summary due_today.items[]:
// id / title / due_at / source_system).
type DueTodayItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	DueAt        time.Time `json:"due_at"`
	SourceSystem string    `json:"source_system"`
}

// DueToday is home/summary's due_today block.
//
// Answerable is the honest verdict about the data source: see HomeSummaryDueToday. It is false
// whenever svc-homeos has no due-date table of its own, and then Count is 0 because nothing was
// counted, NOT because nothing is due. Callers must render 「今日 N 项 · 到期与待办」 only when
// Answerable is true and must not print Count as a real number otherwise.
type DueToday struct {
	Answerable       bool           `json:"-"`
	Count            int64          `json:"count"`
	Items            []DueTodayItem `json:"items"`
	OverBudgetMonths int64          `json:"-"`
	ProjectionAsOf   *time.Time     `json:"-"`
}

// financeBucketSummary is the aggregate row over homeos_proj_finance for one period.
//
// It deliberately holds no timestamp column: MAX(updated_at) loses the declared `timestamp`
// affinity, so the SQLite driver hands back a bare string and database/sql cannot store it into
// a *time.Time, while PostgreSQL hands back a time.Time. Freshness is read as a plain column
// instead (see projectionAsOf), which both engines type the same way.
type financeBucketSummary struct {
	BucketCount      int64 `gorm:"column:bucket_count"`
	OverBudgetMonths int64 `gorm:"column:over_budget_months"`
}

// HomeSummaryDueToday builds home/summary's due_today block.
//
// What the projection can and cannot answer: homeos.homeos_proj_finance (0004) carries
// (family_id, bucket_month, income_cents, expense_cents, budget_remaining_cents, updated_at)
// only -- monthly buckets with no per-day date, and svc-homeos may not read finance's own
// finance_repayment_plan / finance_bill (tech plan §六: 无跨 schema 读、无跨服务事务；投影可由
// 订阅器全量重建). So 「今日到期」 is NOT derivable from the projection and this function does
// not invent it: it takes the due items from homeos_due_registration when that table exists
// (the columns are the ones internal/consumer/finance_consumer.go already writes --
// id/source_system/source_id/kind/due_at/title/family_id -- but no migration creates it yet;
// the S5 到期中心 card owns it), and otherwise reports Answerable=false.
//
// In both branches it returns the one period number the projection really can give: how many
// month buckets inside the period are already over budget (CASE WHEN on
// budget_remaining_cents < 0; NULL means "未设该周期预算" per 定版⑬ and is excluded, not
// counted as 0), plus the freshest updated_at for faces[].as_of (12.3 「截至 HH:MM」).
func HomeSummaryDueToday(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	window DueWindow,
) (DueToday, error) {
	if familyID == "" {
		return DueToday{}, fmt.Errorf("%w: family_id is required", ErrInvalidArgument)
	}
	if window.DayStart.IsZero() || window.DayEnd.IsZero() || !window.DayEnd.After(window.DayStart) {
		return DueToday{}, fmt.Errorf("%w: DayStart/DayEnd must bound a positive range", ErrInvalidArgument)
	}

	result := DueToday{}

	// --- the projection side: always answerable, period-scoped.
	if !window.PeriodFrom.IsZero() && !window.PeriodTo.IsZero() {
		var summary financeBucketSummary
		projTable := "homeos_proj_finance"
		// bucket_month is a `date` (0004); comparing it against bound instants works on
		// PostgreSQL (implicit cast of the driver's timestamp) and on SQLite (ISO text), and
		// keeps this statement engine-neutral -- no date_trunc/TO_CHAR here on purpose.
		if err := db.WithContext(ctx).Raw(
			"SELECT COUNT(*) AS bucket_count, "+
				"COALESCE(SUM(CASE WHEN budget_remaining_cents < 0 THEN 1 ELSE 0 END), 0) AS over_budget_months "+
				"FROM "+projTable+" "+
				"WHERE family_id = ? AND bucket_month >= ? AND bucket_month < ?",
			familyID, window.PeriodFrom, window.PeriodTo,
		).Scan(&summary).Error; err != nil {
			return DueToday{}, fmt.Errorf("failed to summarize finance projection: %w", err)
		}
		result.OverBudgetMonths = summary.OverBudgetMonths

		// faces[].as_of (12.3 「截至 HH:MM」): the projection's last write, newest bucket first.
		var asOf []struct {
			Freshness time.Time `gorm:"column:updated_at"`
		}
		if err := db.WithContext(ctx).Table(projTable).
			Select("updated_at").
			Where("family_id = ? AND bucket_month >= ? AND bucket_month < ?", familyID, window.PeriodFrom, window.PeriodTo).
			Order("updated_at DESC").
			Limit(1).
			Find(&asOf).Error; err != nil {
			return DueToday{}, fmt.Errorf("failed to read finance projection freshness: %w", err)
		}
		if len(asOf) > 0 {
			fresh := asOf[0].Freshness
			result.ProjectionAsOf = &fresh
		}
	}

	// --- the due-center side: only when the due-registration table has been migrated.
	dueTable := "homeos_due_registration"
	if !db.WithContext(ctx).Migrator().HasTable(dueTable) {
		// Nothing was counted; the caller must not paint result.Count as "0 项今日到期".
		return result, nil
	}
	result.Answerable = true

	if err := db.WithContext(ctx).Table(dueTable).
		Where("family_id = ? AND due_at >= ? AND due_at < ?", familyID, window.DayStart, window.DayEnd).
		Count(&result.Count).Error; err != nil {
		return DueToday{}, fmt.Errorf("failed to count due items today: %w", err)
	}
	// 17.2: count is the full day's number, items[] is only the first 3 by due_at ASC --
	// the client neither truncates itself nor adds a fourth card.
	items := make([]DueTodayItem, 0, 3)
	if err := db.WithContext(ctx).Table(dueTable).
		Select("id, title, due_at, source_system").
		Where("family_id = ? AND due_at >= ? AND due_at < ?", familyID, window.DayStart, window.DayEnd).
		Order("due_at ASC").
		Limit(3).
		Scan(&items).Error; err != nil {
		return DueToday{}, fmt.Errorf("failed to list due items today: %w", err)
	}
	result.Items = items
	return result, nil
}
