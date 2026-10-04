// finance_transaction_projection.go is svc-homeos's consumer for the three finance money events, the
// writer that finally feeds homeos.homeos_proj_finance -- the projection 首页's finance cell reads and
// nothing else ever wrote (repo/homeos_projection.go FaceProjectionSummary / handler/faces.go). Before
// this file homeos_proj_finance had 0 rows and no writer anywhere in the repo, so the cell answered
// 「本周期暂无记账」 no matter how much the family booked (PRD 17.2、tech plan §六「该投影同时是
// home/summary 中财务那一格 headline 与角标的唯一数据源」).
//
// The contract is server/contracts/events/finance.yaml:17-58 (FROZEN, v1.0.0), consumers: svc-homeos:
//
//   finance.transaction.created  payload: transaction_id, family_id, type(income|expense|transfer),
//                                amount_cents, category_id, account_id, occurred_at, created_by  (:24-32)
//   finance.transaction.updated  payload: transaction_id, family_id, updated_fields, version       (:41-45)
//   finance.transaction.deleted  payload: transaction_id, family_id, deleted_at, deleted_by         (:53-58)
//
// Sign convention. svc-finance stores an expense amount NEGATED (model/finance.go:67「amount in cents,
// positive for income, negative for expense」, so ¥88.80 of expense is -8880). The projection's reader
// prints it back as a plain money figure: faces.go:236 headlineSpend renders
// 「本周期支出 」 + formatMoney(summary.ExpenseCents) with no sign flip, and FaceProjectionSummary just
// SUMs the column (homeos_projection.go:130). So homeos_proj_finance.expense_cents must hold the
// expense MAGNITUDE (non-negative), not the negated source value -- taking the source amount literally
// would print 「本周期支出 -¥88.80」, the double-negation this card is here to avoid. income_cents
// likewise holds the non-negative income figure; a transfer contributes to neither wing (it is internal
// movement, not spend for the headline's sake).
//
// Idempotency and replay (PRD 10.4、§3.4). Two layers:
//   - bus.DurableConsumer dedupes on (event_type, business_id) BEFORE this handler runs (consumer.go's
//     handleMessage claims homeos_event_dedupe first -- the machinery already installed for
//     finance.due.registered/revoked is what guards these subjects too), so a redelivery of the same
//     created/updated/deleted never reaches it twice.
//   - Independently, the projection is a RECOMPUTE, never an increment. Each transaction's normalized
//     money is mirrored into homeos.homeos_proj_finance_txn (migration 0012, keyed by transaction_id),
//     and homeos_proj_finance's month bucket is rebuilt as SUM over that mirror. Replaying any message
//     once or twice lands on the same bucket value, and deleting a transaction removes its mirror row
//     and recomputes -- so its contribution leaves the total exactly. The bucket is a derived rollup, so
//     it can always be rebuilt from the mirror alone (§六「可随时全量重建」).

package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
)

// The three contract subjects this handler subscribes to (contracts/events/finance.yaml). Each is also
// the durable consumer's FilterSubject; one handler serves all three and dispatches on the envelope's
// own event_type so the money logic lives in exactly one place.
const (
	TransactionCreatedEventType = "finance.transaction.created"
	TransactionUpdatedEventType = "finance.transaction.updated"
	TransactionDeletedEventType = "finance.transaction.deleted"
)

// Projection table names. The aggregate is 0004's; the transaction mirror is 0012's. Both live in this
// service's own schema (search_path=homeos), so the bare names resolve here and nowhere else --
// §六「{code}_proj_* 只允许本服务订阅器写入」.
const (
	projFinanceTable    = "homeos_proj_finance"
	projFinanceTxnTable = "homeos_proj_finance_txn"
	financeTxnMonthFmt  = "2006-01-02"
)

// Refusals. A delivery that cannot supply the fields the bucket is built from is returned as an error
// so §3.4 parks it in the dead letter rather than silently dropping money -- the same posture
// due_revoked_handler.go takes for its anchor fields.
var (
	ErrNoTxnFamilyID = errors.New("finance.transaction.* 缺 family_id")
	ErrNoTxnID       = errors.New("finance.transaction.* 缺 transaction_id")
	ErrNoTxnPayload  = errors.New("finance.transaction.* 信封缺少 payload")
	ErrTxnOccurredAt = errors.New("finance.transaction.created 缺/坏 occurred_at，无法定月桶")
)

// transactionEvent is the flat payload form of the three money events. One struct carries the union of
// their payload_schema keys; encoding/json leaves the absent ones at their zero value, so created
// populates AmountCents/Type/OccurredAt and deleted populates DeletedAt/DeletedBy.
type transactionEvent struct {
	TransactionID string         `json:"transaction_id"`
	FamilyID      string         `json:"family_id"`
	Type          string         `json:"type"`
	AmountCents   int64          `json:"amount_cents"`
	OccurredAt    string         `json:"occurred_at"`
	CreatedBy     string         `json:"created_by"`
	DeletedAt     string         `json:"deleted_at"`
	DeletedBy     string         `json:"deleted_by"`
	Version       int64          `json:"version"`
	UpdatedFields map[string]any `json:"updated_fields"`
}

// FinanceTransactionProjectionHandler is the bus.Handler for the three finance.transaction.* subjects.
func FinanceTransactionProjectionHandler(db *gorm.DB) bus.Handler {
	return func(ctx context.Context, msg bus.Message) error {
		if len(msg.Envelope.Payload) == 0 {
			return ErrNoTxnPayload
		}
		raw, err := json.Marshal(msg.Envelope.Payload)
		if err != nil {
			return fmt.Errorf("序列化 %s 的 payload: %w", msg.Envelope.EventType, err)
		}
		var ev transactionEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return fmt.Errorf("解析 %s 的 payload: %w", msg.Envelope.EventType, err)
		}

		switch msg.Envelope.EventType {
		case TransactionCreatedEventType:
			return applyTransactionCreated(ctx, db, &ev)
		case TransactionUpdatedEventType:
			return applyTransactionUpdated(ctx, db, &ev)
		case TransactionDeletedEventType:
			return applyTransactionDeleted(ctx, db, &ev)
		default:
			// Not this handler's subject. The durable consumer's FilterSubject never routes another
			// subject here in production; returning nil (not an error) keeps a mis-subscription from
			// dead-lettering money it does not own.
			return nil
		}
	}
}

// applyTransactionCreated mirrors one created transaction into homeos_proj_finance_txn and recomputes
// its month bucket. business_id is {transaction_id} (finance.yaml:19), so the mirror's PK doubles as
// the replay anchor: a redelivery upserts the same row to the same values, and the recompute below
// rebuilds (never adds to) the aggregate.
func applyTransactionCreated(ctx context.Context, db *gorm.DB, ev *transactionEvent) error {
	if ev.TransactionID == "" {
		return ErrNoTxnID
	}
	familyID := ev.FamilyID
	if familyID == "" {
		return ErrNoTxnFamilyID
	}
	occurredAt, err := parseOccurredAt(ev.OccurredAt)
	if err != nil {
		return ErrTxnOccurredAt
	}
	income, expense := normalizeMoney(ev.Type, ev.AmountCents)
	bucket := bucketMonthOf(occurredAt)

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := upsertTxnMirror(ctx, tx, ev.TransactionID, familyID, bucket, income, expense, occurredAt); err != nil {
			return err
		}
		return recomputeFinanceBucket(ctx, tx, familyID, bucket)
	})
}

// applyTransactionUpdated re-derives the mirror when the update carries money fields, else refreshes
// the affected bucket. finance.yaml:44 hands updated as an opaque updated_fields object plus a version;
// the amount may or may not be present depending on what the publisher chose to send. When it is
// (top-level or inside updated_fields), the mirror is re-upserted -- an edit that changes the amount or
// the month moves/rewrites the contribution and both the old and the new bucket are recomputed. When it
// is not (a category-only edit), only the transaction's current family is refreshed (no-op on totals,
// keeps updated_at honest). Cross-family edits recompute both sides.
func applyTransactionUpdated(ctx context.Context, db *gorm.DB, ev *transactionEvent) error {
	if ev.TransactionID == "" {
		return ErrNoTxnID
	}
	familyID := ev.FamilyID
	if familyID == "" {
		return ErrNoTxnFamilyID
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		oldFamily, found := readMirrorFamily(ctx, tx, ev.TransactionID)

		if !hasAnyMoneyKey(ev) {
			if found {
				return recomputeFinanceFamily(ctx, tx, oldFamily)
			}
			return nil
		}

		occurredAt := resolveOccurredAt(ev)
		if occurredAt.IsZero() {
			if t, ok := readMirrorOccurredAt(ctx, tx, ev.TransactionID); ok {
				occurredAt = t
			} else {
				return ErrTxnOccurredAt
			}
		}
		inc, exp := normalizeMoney(resolveType(ev), resolveAmount(ev))
		bucket := bucketMonthOf(occurredAt)

		if err := upsertTxnMirror(ctx, tx, ev.TransactionID, familyID, bucket, inc, exp, occurredAt); err != nil {
			return err
		}
		if err := recomputeFinanceBucket(ctx, tx, familyID, bucket); err != nil {
			return err
		}
		if found && oldFamily != familyID {
			return recomputeFinanceFamily(ctx, tx, oldFamily)
		}
		return nil
	})
}

// applyTransactionDeleted removes the mirror and recomputes, so a soft-deleted transaction's money
// leaves the projection (PRD 10.4「删除要撤掉贡献」). Deleting a transaction that was never mirrored --
// or deleting twice -- finds no row and changes nothing, which is the idempotent outcome, not an error.
func applyTransactionDeleted(ctx context.Context, db *gorm.DB, ev *transactionEvent) error {
	if ev.TransactionID == "" {
		return ErrNoTxnID
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		oldFamily, found := readMirrorFamily(ctx, tx, ev.TransactionID)
		if !found {
			return nil
		}
		res := tx.WithContext(ctx).Exec(
			"DELETE FROM "+projFinanceTxnTable+" WHERE transaction_id = ?", ev.TransactionID)
		if res.Error != nil {
			return fmt.Errorf("删除流水镜像失败: %w", res.Error)
		}
		return recomputeFinanceFamily(ctx, tx, oldFamily)
	})
}

// upsertTxnMirror writes one transaction's normalized money into the mirror, keyed by transaction_id
// so a redelivery rewrites the same row instead of adding a second one (§3.4「重复投递不产生重复业务
// 对象」). bucket_month is written as the 'YYYY-MM-01' text 0004 stores in a date column (PG casts text
// to date on assignment; the tests' sqlite stores it verbatim) so the same value round-trips on both.
func upsertTxnMirror(ctx context.Context, tx *gorm.DB, txnID, familyID, bucket string, income, expense int64, occurredAt time.Time) error {
	err := tx.WithContext(ctx).Exec(
		"INSERT INTO "+projFinanceTxnTable+
			" (transaction_id, family_id, bucket_month, income_cents, expense_cents, occurred_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT (transaction_id) DO UPDATE SET "+
			"family_id = EXCLUDED.family_id, bucket_month = EXCLUDED.bucket_month, "+
			"income_cents = EXCLUDED.income_cents, expense_cents = EXCLUDED.expense_cents, "+
			"occurred_at = EXCLUDED.occurred_at, updated_at = EXCLUDED.updated_at",
		txnID, familyID, bucket, income, expense, occurredAt.UTC(), time.Now().UTC()).Error
	if err != nil {
		return fmt.Errorf("写入流水镜像失败: %w", err)
	}
	return nil
}

// recomputeFinanceBucket rebuilds ONE (family, bucket_month) row in homeos_proj_finance as SUM over the
// mirror -- never an increment. Two statements inside the caller's transaction:
//
//   - If the mirror has no rows left for that bucket, drop the aggregate row: the finance cell then
//     sees BucketCount 0 and falls through to headlineNoData 「本周期暂无记账」, which is the honest
//     answer after every booking in that month was deleted (repo/homeos_projection.go:66 HasData /
//     faces.go:193).
//   - Otherwise upsert the aggregate from the mirror's SUM, refreshing income_cents/expense_cents and
//     updated_at. budget_remaining_cents is intentionally NOT touched (0004:24 makes NULL the 「未设该
//     周期预算」 state; money events say nothing about budget, and faces.go then reads a bucket with no
//     budget as headlineSpend 「本周期支出 ¥X」 -- the behaviour the task requires for a family with
//     transactions but no budget).
func recomputeFinanceBucket(ctx context.Context, tx *gorm.DB, familyID, bucket string) error {
	if err := tx.WithContext(ctx).Exec(
		"DELETE FROM "+projFinanceTable+" WHERE family_id = ? AND bucket_month = ? "+
			"AND NOT EXISTS (SELECT 1 FROM "+projFinanceTxnTable+" d "+
			"WHERE d.family_id = ? AND d.bucket_month = ?)",
		familyID, bucket, familyID, bucket).Error; err != nil {
		return fmt.Errorf("清理空月桶失败: %w", err)
	}

	now := time.Now().UTC()
	err := tx.WithContext(ctx).Exec(
		"INSERT INTO "+projFinanceTable+
			" (family_id, bucket_month, income_cents, expense_cents, budget_remaining_cents, updated_at) "+
			"SELECT d.family_id, d.bucket_month, COALESCE(SUM(d.income_cents), 0), COALESCE(SUM(d.expense_cents), 0), NULL, ? "+
			"FROM "+projFinanceTxnTable+" d WHERE d.family_id = ? AND d.bucket_month = ? "+
			"GROUP BY d.family_id, d.bucket_month "+
			"ON CONFLICT (family_id, bucket_month) DO UPDATE SET "+
			"income_cents = EXCLUDED.income_cents, expense_cents = EXCLUDED.expense_cents, updated_at = EXCLUDED.updated_at",
		now, familyID, bucket).Error
	if err != nil {
		return fmt.Errorf("重算月桶失败: %w", err)
	}
	return nil
}

// recomputeFinanceFamily rebuilds EVERY month bucket of one family in one pass from the mirror. It is
// the shape used by the update/delete paths where the target bucket is on the mirror side rather than
// in the event payload (finance.yaml:41-58 hands updated / deleted only transaction_id + family_id, and
// a moved-month update affects the old bucket while a delete affects whichever bucket the row lived
// in -- both are the family's rows). Same two-statement rule as recomputeFinanceBucket: drop empties,
// upsert from SUM; budget_remaining_cents stays NULL/untouched.
func recomputeFinanceFamily(ctx context.Context, tx *gorm.DB, familyID string) error {
	if err := tx.WithContext(ctx).Exec(
		"DELETE FROM "+projFinanceTable+" WHERE family_id = ? "+
			"AND NOT EXISTS (SELECT 1 FROM "+projFinanceTxnTable+" d "+
			"WHERE d.family_id = ? AND d.bucket_month = "+projFinanceTable+".bucket_month)",
		familyID, familyID).Error; err != nil {
		return fmt.Errorf("清理空月桶失败: %w", err)
	}

	now := time.Now().UTC()
	err := tx.WithContext(ctx).Exec(
		"INSERT INTO "+projFinanceTable+
			" (family_id, bucket_month, income_cents, expense_cents, budget_remaining_cents, updated_at) "+
			"SELECT d.family_id, d.bucket_month, COALESCE(SUM(d.income_cents), 0), COALESCE(SUM(d.expense_cents), 0), NULL, ? "+
			"FROM "+projFinanceTxnTable+" d WHERE d.family_id = ? "+
			"GROUP BY d.family_id, d.bucket_month "+
			"ON CONFLICT (family_id, bucket_month) DO UPDATE SET "+
			"income_cents = EXCLUDED.income_cents, expense_cents = EXCLUDED.expense_cents, updated_at = EXCLUDED.updated_at",
		now, familyID).Error
	if err != nil {
		return fmt.Errorf("重算家庭月桶失败: %w", err)
	}
	return nil
}

// readMirrorFamily returns the family_id the mirror currently holds for one transaction, or found=false
// when the transaction is not mirrored. Plain string -- no date-typing hazard.
func readMirrorFamily(ctx context.Context, tx *gorm.DB, txnID string) (string, bool) {
	var row struct {
		FamilyID string `gorm:"column:family_id"`
	}
	err := tx.WithContext(ctx).Raw(
		"SELECT family_id FROM "+projFinanceTxnTable+" WHERE transaction_id = ?", txnID).Scan(&row).Error
	if err != nil || row.FamilyID == "" {
		return "", false
	}
	return row.FamilyID, true
}

// readMirrorOccurredAt fetches the mirror's stored occurred_at so an update that omits it can bucket by
// the transaction's original month rather than a fabricated one.
func readMirrorOccurredAt(ctx context.Context, tx *gorm.DB, txnID string) (time.Time, bool) {
	var row struct {
		OccurredAt time.Time `gorm:"column:occurred_at"`
	}
	err := tx.WithContext(ctx).Raw(
		"SELECT occurred_at FROM "+projFinanceTxnTable+" WHERE transaction_id = ?", txnID).Scan(&row).Error
	if err != nil || row.OccurredAt.IsZero() {
		return time.Time{}, false
	}
	return row.OccurredAt, true
}

// normalizeMoney turns one transaction's source amount into the projection's two non-negative wings.
// svc-finance negates expenses (model/finance.go:67), so the expense magnitude is the absolute value
// and is stored positive; income is stored as its magnitude. A transfer is internal movement and hits
// neither wing -- 本周期支出/收入 must not double-count money that only moved between the family's own
// accounts.
func normalizeMoney(txnType string, amountCents int64) (income, expense int64) {
	abs := amountCents
	if abs < 0 {
		abs = -abs
	}
	switch txnType {
	case "income":
		return abs, 0
	case "expense":
		return 0, abs
	case "transfer":
		return 0, 0
	default:
		// Unknown/absent type: fall back to the sign svc-finance itself uses (negative -> expense wing),
		// so a producer that omits type still buckets into money rather than being dropped.
		if amountCents < 0 {
			return 0, abs
		}
		return abs, 0
	}
}

func parseOccurredAt(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, ErrTxnOccurredAt
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, ErrTxnOccurredAt
}

// bucketMonthOf renders the first day of occurred_at's month as the 'YYYY-MM-01' text 0004 stores in
// bucket_month and the projection reader compares against (homeos_projection.go:134).
func bucketMonthOf(t time.Time) string {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).Format(financeTxnMonthFmt)
}

// hasAnyMoneyKey reports whether an update carries at least one money-bearing field, top-level or inside
// updated_fields; if none is present the update cannot change money and only refreshes the bucket.
func hasAnyMoneyKey(ev *transactionEvent) bool {
	if ev.Type != "" || ev.OccurredAt != "" || ev.AmountCents != 0 {
		return true
	}
	for _, k := range []string{"type", "amount_cents", "occurred_at"} {
		if _, ok := ev.UpdatedFields[k]; ok {
			return true
		}
	}
	return false
}

func resolveType(ev *transactionEvent) string {
	if ev.Type != "" {
		return ev.Type
	}
	return stringField(ev.UpdatedFields, "type")
}

func resolveAmount(ev *transactionEvent) int64 {
	if ev.AmountCents != 0 {
		return ev.AmountCents
	}
	if v, ok := ev.UpdatedFields["amount_cents"]; ok {
		return int64Field(v)
	}
	return ev.AmountCents
}

func resolveOccurredAt(ev *transactionEvent) time.Time {
	if t, err := parseOccurredAt(ev.OccurredAt); err == nil {
		return t
	}
	if s := stringField(ev.UpdatedFields, "occurred_at"); s != "" {
		if t, err := parseOccurredAt(s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// int64Field coerces a JSON number (which unmarshals to float64) into an int64 cents value.
func int64Field(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
