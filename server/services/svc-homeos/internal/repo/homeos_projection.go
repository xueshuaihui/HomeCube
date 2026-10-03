// homeos_projection.go holds the ONE read home/summary's face cells need from this service's local
// projections of another domain (tech plan §六 「{code}_proj_{src} … 只允许本服务订阅器写入」).
//
// It is a separate file rather than more lines in homeos.go because homeos.go is another card's
// working area in this round (the /dynamics and /notifications handlers read it), and because this
// query is not a dynamics/notification query at all: it is the 首页 C 区 aggregate over the monthly
// buckets 0004 declared.
//
// Why a new query was needed at all: repo.HomeSummaryDueToday (homeos.go:808) already reads the same
// table, but it returns only the period's over-budget month COUNT and the freshest updated_at -- it
// deliberately does not sum income/expense/budget, because its own doc comment scopes it to the
// due_today block. PRD 17.2 and tech plan §六 make those sums the finance cell's 状态句 content
// (「该投影同时是 home/summary 中财务那一格 headline 与角标的唯一数据源」), so the face cell needed the
// numbers the existing function does not carry. Nothing here edits an existing query.
//
// No face code is spelled in this file: the table name comes from
// registry.HomeosCode's Domain.ProjectionTable(src) with src being the catalog row the handler is
// currently rendering (tech plan §2.3, PRD 16.3), so the handler never hands this function a name it
// invented and a second projection table needs a second reader, not a second literal.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"gorm.io/gorm"
)

// ErrProjectionMissing says "this checkout has no local projection table for that face yet". It is a
// distinct sentinel because the cell's answer differs: a missing table says the number was never
// computed by anyone (the subscriber for that domain has not landed), an empty table says it was
// computed and this family simply has no buckets in the period. Declared here rather than in
// homeos.go's var block so this card adds no line to a file another card is editing.
var ErrProjectionMissing = errors.New("face projection table is not available in this checkout")

// FaceProjection is one face cell's period aggregate over that domain's monthly buckets.
//
// Field meanings are 0004's column set, read verbatim:
//   - BucketCount: months of the period the projection covers. 0 means "no data for this period",
//     which is NOT "0 元" and is what keeps the headline from printing a money figure nobody has.
//   - BudgetBuckets: how many of those buckets carry a budget at all. 0004 makes
//     budget_remaining_cents NULL exactly when 该周期未设预算 (定版⑬), so BudgetBucketCount < BucketCount
//     is the "只有月度预算" state the 季/年 档 has to surface as 「未设该周期预算」 rather than as a
//     number multiplied out of a monthly budget.
//   - BudgetRemainingCents: the sum over the NON-NULL buckets, nil when there is no such bucket.
//     Summing existing monthly remainders is the 服务端在月桶上求和 tech plan §六 requires for the
//     季/年 档; multiplying or scaling a budget is what 定版⑬ forbids, and nothing here does it.
//   - OverBudgetMonths: buckets whose remainder went negative -- the same CASE WHEN the due_today
//     block already uses, so the two readings of the same table cannot disagree.
//   - AsOf: the newest updated_at inside the period (12.3 「截至 HH:MM」).
type FaceProjection struct {
	BucketCount          int64      `gorm:"column:bucket_count"`
	BudgetBuckets        int64      `gorm:"column:budget_buckets"`
	IncomeCents          int64      `gorm:"column:income_cents"`
	ExpenseCents         int64      `gorm:"column:expense_cents"`
	BudgetRemainingCents *int64     `gorm:"column:budget_remaining_cents"`
	OverBudgetMonths     int64      `gorm:"column:over_budget_months"`
	AsOf                 *time.Time `gorm:"-"`
}

// HasData reports whether the projection holds any bucket of this period for this family. It is the
// only honest answer to "can this face's cell state a number".
func (p FaceProjection) HasData() bool { return p.BucketCount > 0 }

// faceProjectionTable derives this service's local projection table for one face code out of the
// registry (PRD 16.3 「{code}_proj_{来源域}」). An unregistered code is refused rather than
// concatenated, so the name that reaches the SQL string is always one of the seven rows.
func faceProjectionTable(src string) (string, error) {
	homeos, ok := registry.ByCode(registry.HomeosCode)
	if !ok {
		return "", fmt.Errorf("%w: registry has no %q row", ErrInvalidArgument, registry.HomeosCode)
	}
	if _, ok := registry.ByCode(src); !ok {
		return "", fmt.Errorf("%w: face code %q is not registered", ErrInvalidArgument, src)
	}
	return homeos.ProjectionTable(src), nil
}

// ProjectionExists reports whether this checkout's migration sequence has created the projection
// table for one face. The home screen uses it to decide which cells have a server-side data source
// at all, so "no table" is read from the schema rather than from a hand-listed set of faces -- and a
// face born in P2 whose projection has not landed simply gets no number instead of an invented one.
func ProjectionExists(ctx context.Context, db *gorm.DB, src string) (bool, error) {
	table, err := faceProjectionTable(src)
	if err != nil {
		return false, err
	}
	return db.WithContext(ctx).Migrator().HasTable(table), nil
}

// FaceProjectionSummary sums one family's monthly buckets inside [from, to) for one face.
//
// The window is passed as instants and compared against bucket_month exactly the way
// HomeSummaryDueToday already does (0004 stores bucket_month as a `date` holding the month's first
// day), so the statement stays engine-neutral: no date_trunc, no TO_CHAR, no driver-specific cast.
//
// The table is read only when it exists; a missing projection is reported as ErrProjectionMissing so
// the caller degrades that ONE cell instead of failing the whole 首屏 request (17.2 首屏单请求: the
// other three zones must still land).
func FaceProjectionSummary(
	ctx context.Context,
	db *gorm.DB,
	familyID string,
	src string,
	from time.Time,
	to time.Time,
) (FaceProjection, error) {
	var out FaceProjection
	if familyID == "" {
		return out, fmt.Errorf("%w: family_id is required", ErrInvalidArgument)
	}
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return out, fmt.Errorf("%w: from/to must bound a positive range", ErrInvalidArgument)
	}
	table, err := faceProjectionTable(src)
	if err != nil {
		return out, err
	}
	if !db.WithContext(ctx).Migrator().HasTable(table) {
		return out, fmt.Errorf("%w: projection table %q is not migrated", ErrProjectionMissing, table)
	}

	err = db.WithContext(ctx).Raw(
		"SELECT COUNT(*) AS bucket_count, "+
			"COALESCE(COUNT(budget_remaining_cents), 0) AS budget_buckets, "+
			"COALESCE(SUM(income_cents), 0) AS income_cents, "+
			"COALESCE(SUM(expense_cents), 0) AS expense_cents, "+
			"SUM(budget_remaining_cents) AS budget_remaining_cents, "+
			"COALESCE(SUM(CASE WHEN budget_remaining_cents < 0 THEN 1 ELSE 0 END), 0) AS over_budget_months "+
			"FROM "+table+" "+
			"WHERE family_id = ? AND bucket_month >= ? AND bucket_month < ?",
		familyID, from, to,
	).Scan(&out).Error
	if err != nil {
		return out, fmt.Errorf("failed to summarize projection %q: %w", table, err)
	}

	// Freshness as a plain column, not MAX(updated_at): 0004's updated_at loses its declared
	// timestamp affinity through an aggregate on the SQLite driver (the same reason
	// financeBucketSummary carries no time column), while PostgreSQL hands back a time.Time. Reading
	// the newest row's value types identically on both engines.
	var fresh []struct {
		UpdatedAt time.Time `gorm:"column:updated_at"`
	}
	if err := db.WithContext(ctx).Table(table).
		Select("updated_at").
		Where("family_id = ? AND bucket_month >= ? AND bucket_month < ?", familyID, from, to).
		Order("updated_at DESC").
		Limit(1).
		Find(&fresh).Error; err != nil {
		return out, fmt.Errorf("failed to read projection %q freshness: %w", table, err)
	}
	if len(fresh) > 0 {
		at := fresh[0].UpdatedAt
		out.AsOf = &at
	}
	return out, nil
}
