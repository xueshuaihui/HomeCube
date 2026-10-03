// Package repo provides data access operations for the finance service.
package repo

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/packages/sync"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"gorm.io/gorm"
)

var (
	ErrAccountBalanceNonZero = errors.New("account balance must be zero before archiving")
	ErrOptimisticLock        = errors.New("optimistic lock conflict: record was modified by another request")
	ErrDuplicateRequest      = errors.New("duplicate request: this transaction has already been processed")
)

// isUniqueViolation reports a unique-index collision in an engine-neutral way, the same shape as
// svc-homeos's helper (services/svc-homeos/internal/repo/homeos.go:218). This repo runs on
// PostgreSQL in production and on SQLite in the tests, and neither driver's error type is imported
// here for one check:
//
//   - postgres/pgx: 「duplicate key value violates unique constraint "uk_finance_..." (SQLSTATE 23505)」
//   - sqlite:       「UNIQUE constraint failed: finance_transaction.client_request_id」
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "violates unique constraint") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "sqlstate 23505")
}

// isDuplicateClientRequestKey narrows that collision down to the idempotency key. Both engines name
// the key in the message -- sqlite the column, postgres the index built on it
// (uk_finance_transaction_client_request_id) -- so a unique violation on some other index, above all
// a generated primary key, stays an ordinary server fault instead of telling the client its request
// was already processed.
func isDuplicateClientRequestKey(err error) bool {
	if !isUniqueViolation(err) {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "client_request_id")
}

// The 到期撤销 half of PRD 卷首第 3 条（「带时间语义的业务对象一律注册到 HomeOS 的日历/待办/提醒」）:
// a registration needs a counter-event, otherwise a paid bill's 到期 row stays in 首页 B 区 and in
// 到期中心 forever. contracts/events/finance.yaml:93-105 (FROZEN, v1.0.0) names it
// finance.due.revoked, and the identifiers below are that entry's own words, not new ones:
//
//   - subject == event name（§3.1「subject 命名即事件名：{code}.{object}.{action}」）
//   - business_id == "{source_id}:{due_at}"（finance.yaml:95 + contracts/README.md:29 的幂等键口径）
//   - payload == source_system / source_id / family_id / revoked_at / reason（finance.yaml:100-105）
//   - reason ∈ enum(completed|deleted|expired)（finance.yaml:105）；账单结清是 completed。
//
// financeDomainCode is both the outbox table prefix（bus.OutboxTableName → finance_outbox,
// finance_0002）and the contract's payload source_system —— homeos_0008:29 defines source_system as
// 「注册该到期对象的域 code（PRD 16.1）」, and registry's finance row is the same string.
const (
	subjectDueRevoked      = "finance.due.revoked"
	financeDomainCode      = "finance"
	revokedReasonCompleted = "completed"
	// dueEventSchemaVersion mirrors the version the due-registration producer stamps
	// (svc-finance internal/service/bill.go) per §3.1「版本不进 subject 而进信封 version 字段」.
	dueEventSchemaVersion = "1.0"
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

// FinanceRepo provides CRUD operations for finance entities.
type FinanceRepo struct {
	db   *gorm.DB
	sync *sync.Repo
}

// NewFinanceRepo creates a new finance repository instance.
func NewFinanceRepo(db *gorm.DB) *FinanceRepo {
	return &FinanceRepo{
		db:   db,
		sync: sync.NewRepo(db, "finance"),
	}
}

// ==================== Account Operations ====================

// CreateAccount creates a new finance account.
func (r *FinanceRepo) CreateAccount(ctx context.Context, account *model.FinanceAccount) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if account.ID == "" {
			account.ID = generateUUID()
		}
		if err := tx.Create(account).Error; err != nil {
			return fmt.Errorf("failed to create account: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, account.FamilyID, "account", account.ID, "CREATE", account.Version, account)
	})
}

// GetAccountByID retrieves an account by its ID.
func (r *FinanceRepo) GetAccountByID(ctx context.Context, id string) (*model.FinanceAccount, error) {
	var account model.FinanceAccount
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&account)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get account: %w", result.Error)
	}
	return &account, nil
}

// ListAccountsByFamily retrieves all accounts for a family.
func (r *FinanceRepo) ListAccountsByFamily(ctx context.Context, familyID string) ([]model.FinanceAccount, error) {
	var accounts []model.FinanceAccount
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		Order("created_at DESC").
		Find(&accounts)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", result.Error)
	}
	return accounts, nil
}

// ArchiveAccount archives an account (only if balance is zero).
func (r *FinanceRepo) ArchiveAccount(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account model.FinanceAccount
		result := tx.Where("id = ?", id).First(&account)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("account not found")
			}
			return fmt.Errorf("failed to get account: %w", result.Error)
		}

		if account.Balance != 0 {
			return ErrAccountBalanceNonZero
		}

		now := time.Now()
		account.IsArchived = true
		account.ArchivedAt = &now
		account.Version++

		result = tx.Save(&account)
		if result.Error != nil {
			return fmt.Errorf("failed to archive account: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, account.FamilyID, "account", account.ID, "UPDATE", account.Version, account)
	})
}

// ==================== Category Operations ====================

// CreateCategory creates a new finance category.
func (r *FinanceRepo) CreateCategory(ctx context.Context, category *model.FinanceCategory) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if category.ID == "" {
			category.ID = generateUUID()
		}
		if err := tx.Create(category).Error; err != nil {
			return fmt.Errorf("failed to create category: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, category.FamilyID, "category", category.ID, "CREATE", category.Version, category)
	})
}

// GetCategoryByID retrieves a category by its ID.
func (r *FinanceRepo) GetCategoryByID(ctx context.Context, id string) (*model.FinanceCategory, error) {
	var category model.FinanceCategory
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&category)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get category: %w", result.Error)
	}
	return &category, nil
}

// ListCategoriesByFamily retrieves all categories for a family.
func (r *FinanceRepo) ListCategoriesByFamily(ctx context.Context, familyID string, isActive *bool) ([]model.FinanceCategory, error) {
	query := r.db.WithContext(ctx).Where("family_id = ? AND deleted_at IS NULL", familyID)
	if isActive != nil {
		query = query.Where("is_active = ?", *isActive)
	}
	query = query.Order("sort_order ASC, created_at DESC")

	var categories []model.FinanceCategory
	result := query.Find(&categories)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list categories: %w", result.Error)
	}
	return categories, nil
}

// DeactivateCategory deactivates a category (does not affect historical transactions).
func (r *FinanceRepo) DeactivateCategory(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var category model.FinanceCategory
		result := tx.Where("id = ?", id).First(&category)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("category not found")
			}
			return fmt.Errorf("failed to get category: %w", result.Error)
		}

		category.IsActive = false
		category.Version++

		result = tx.Save(&category)
		if result.Error != nil {
			return fmt.Errorf("failed to deactivate category: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, category.FamilyID, "category", category.ID, "UPDATE", category.Version, category)
	})
}

// ==================== Transaction Operations ====================

// CreateTransactionInput contains input for creating a transaction.
type CreateTransactionInput struct {
	Transaction *model.FinanceTransaction
	ClientReqID string
	FamilyID    string
	RequestData interface{}
}

// CreateTransaction creates a new finance transaction with idempotency and optimistic locking.
func (r *FinanceRepo) CreateTransaction(ctx context.Context, input CreateTransactionInput) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Check idempotency
		if input.ClientReqID != "" {
			exists, _, err := r.sync.CheckIdempotency(ctx, tx, input.ClientReqID, input.FamilyID, input.RequestData)
			if err != nil {
				return fmt.Errorf("failed to check idempotency: %w", err)
			}
			if exists {
				return ErrDuplicateRequest
			}
		}

		// Generate ID if not set
		if input.Transaction.ID == "" {
			input.Transaction.ID = generateUUID()
		}

		// Create transaction
		if err := tx.Create(input.Transaction).Error; err != nil {
			// The database's own replay guard: uk_client_request_id (model/finance.go:74,
			// migrations/finance/finance_0001:83) rejects a second row for a key that was already
			// processed. The idempotency check above does NOT catch every replay -- when the retry
			// carries the same key but a changed body, sync.CheckIdempotency declines it as
			// "not a replay" (packages/sync/repo.go:196-198, request_hash mismatch) and the flow
			// arrives here. Returning the driver error as "failed to create transaction: %w" makes
			// the HTTP layer answer 500 for what is a duplicate submission; classify it onto the
			// sentinel so CreateTransaction has one duplicate-request answer whichever guard fired.
			if input.ClientReqID != "" && isDuplicateClientRequestKey(err) {
				return fmt.Errorf("client_request_id %q was already processed: %w: %w",
					input.ClientReqID, ErrDuplicateRequest, err)
			}
			return fmt.Errorf("failed to create transaction: %w", err)
		}

		// Append change log
		if err := r.sync.AppendChangeLog(ctx, tx, input.FamilyID, "transaction", input.Transaction.ID, "CREATE", input.Transaction.Version, input.Transaction); err != nil {
			return err
		}

		// Record idempotency key
		if input.ClientReqID != "" {
			if err := r.sync.RecordIdempotency(ctx, tx, input.ClientReqID, input.FamilyID, input.RequestData, input.Transaction); err != nil {
				return fmt.Errorf("failed to record idempotency: %w", err)
			}
		}

		return nil
	})
}

// GetTransactionByID retrieves a transaction by its ID.
func (r *FinanceRepo) GetTransactionByID(ctx context.Context, id string) (*model.FinanceTransaction, error) {
	var transaction model.FinanceTransaction
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&transaction)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get transaction: %w", result.Error)
	}
	return &transaction, nil
}

// ListTransactionsByFamily retrieves transactions for a family with cursor-based pagination.
func (r *FinanceRepo) ListTransactionsByFamily(ctx context.Context, familyID string, period string, cursor *string, limit int) ([]model.FinanceTransaction, *string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	query := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	// Filter by period if provided (format: YYYY-MM)
	if period != "" {
		query = query.Where("TO_CHAR(occurred_at, 'YYYY-MM') = ?", period)
	}

	// Cursor-based pagination (ID-based for simplicity)
	if cursor != nil && *cursor != "" {
		query = query.Where("id < ?", *cursor)
	}

	query = query.Order("id DESC").Limit(limit + 1)

	var transactions []model.FinanceTransaction
	result := query.Find(&transactions)
	if result.Error != nil {
		return nil, nil, fmt.Errorf("failed to list transactions: %w", result.Error)
	}

	// Determine if there are more results
	var nextCursor *string
	if len(transactions) > limit {
		transactions = transactions[:limit]
		nextCursorVal := transactions[len(transactions)-1].ID
		nextCursor = &nextCursorVal
	}

	return transactions, nextCursor, nil
}

// UpdateTransaction updates a transaction with optimistic locking.
func (r *FinanceRepo) UpdateTransaction(ctx context.Context, id string, updates map[string]interface{}, expectedVersion int64) (*model.FinanceTransaction, error) {
	return nil, r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transaction model.FinanceTransaction
		result := tx.Where("id = ?", id).First(&transaction)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("transaction not found")
			}
			return fmt.Errorf("failed to get transaction: %w", result.Error)
		}

		// Optimistic lock check
		if transaction.Version != expectedVersion {
			return ErrOptimisticLock
		}

		// Apply updates
		updates["version"] = expectedVersion + 1
		updates["updated_at"] = time.Now()

		result = tx.Model(&transaction).Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("failed to update transaction: %w", result.Error)
		}

		// Reload to get updated values
		result = tx.Where("id = ?", id).First(&transaction)
		if result.Error != nil {
			return fmt.Errorf("failed to reload transaction: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, transaction.FamilyID, "transaction", transaction.ID, "UPDATE", transaction.Version, transaction)
	})
}

// SoftDeleteTransaction soft-deletes a transaction.
func (r *FinanceRepo) SoftDeleteTransaction(ctx context.Context, id string, deletedBy string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transaction model.FinanceTransaction
		result := tx.Where("id = ?", id).First(&transaction)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("transaction not found")
			}
			return fmt.Errorf("failed to get transaction: %w", result.Error)
		}

		transaction.DeletedBy = &deletedBy
		transaction.Version++

		result = tx.Where("id = ?", id).Delete(&transaction)
		if result.Error != nil {
			return fmt.Errorf("failed to delete transaction: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, transaction.FamilyID, "transaction", transaction.ID, "DELETE", transaction.Version, transaction)
	})
}

// ==================== Ledger Operations ====================

// CreateLedger creates a new finance ledger.
func (r *FinanceRepo) CreateLedger(ctx context.Context, ledger *model.FinanceLedger) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if ledger.ID == "" {
			ledger.ID = generateUUID()
		}
		if err := tx.Create(ledger).Error; err != nil {
			return fmt.Errorf("failed to create ledger: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, ledger.FamilyID, "ledger", ledger.ID, "CREATE", ledger.Version, ledger)
	})
}

// GetLedgerByID retrieves a ledger by its ID.
func (r *FinanceRepo) GetLedgerByID(ctx context.Context, id string) (*model.FinanceLedger, error) {
	var ledger model.FinanceLedger
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&ledger)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get ledger: %w", result.Error)
	}
	return &ledger, nil
}

// ListLedgersByFamily retrieves all ledgers for a family.
func (r *FinanceRepo) ListLedgersByFamily(ctx context.Context, familyID string) ([]model.FinanceLedger, error) {
	var ledgers []model.FinanceLedger
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		Order("sort_order ASC, created_at DESC").
		Find(&ledgers)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list ledgers: %w", result.Error)
	}
	return ledgers, nil
}

// ==================== Budget Operations ====================

// CreateBudget creates a new finance budget.
func (r *FinanceRepo) CreateBudget(ctx context.Context, budget *model.FinanceBudget) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if budget.ID == "" {
			budget.ID = generateUUID()
		}
		if err := tx.Create(budget).Error; err != nil {
			return fmt.Errorf("failed to create budget: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, budget.FamilyID, "budget", budget.ID, "CREATE", budget.Version, budget)
	})
}

// GetBudgetByID retrieves a budget by its ID.
func (r *FinanceRepo) GetBudgetByID(ctx context.Context, id string) (*model.FinanceBudget, error) {
	var budget model.FinanceBudget
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&budget)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get budget: %w", result.Error)
	}
	return &budget, nil
}

// ListBudgetsByFamily retrieves all budgets for a family with optional period filter.
func (r *FinanceRepo) ListBudgetsByFamily(ctx context.Context, familyID string, period *string) ([]model.FinanceBudget, error) {
	query := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	if period != nil && *period != "" {
		query = query.Where("period = ?", *period)
	}

	query = query.Order("created_at DESC")

	var budgets []model.FinanceBudget
	result := query.Find(&budgets)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list budgets: %w", result.Error)
	}
	return budgets, nil
}

// CheckExceeded checks if expenses exceed the budget for a given category and period.
// Returns the total spent amount and whether it exceeds the budget.
func (r *FinanceRepo) CheckExceeded(ctx context.Context, familyID, categoryID string, startDate, endDate time.Time) (int64, bool, error) {
	var totalSpent int64
	err := r.db.WithContext(ctx).
		Model(&model.FinanceTransaction{}).
		Select("COALESCE(SUM(ABS(amount_cents)), 0)").
		Where("family_id = ? AND category_id = ? AND type = 'expense' AND occurred_at >= ? AND occurred_at <= ? AND deleted_at IS NULL",
			familyID, categoryID, startDate, endDate).
		Scan(&totalSpent).Error

	if err != nil {
		return 0, false, fmt.Errorf("failed to check exceeded budget: %w", err)
	}

	return totalSpent, false, nil
}

// ==================== Bill Operations ====================

// CreateBill creates a new finance bill.
func (r *FinanceRepo) CreateBill(ctx context.Context, bill *model.FinanceBill) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if bill.ID == "" {
			bill.ID = generateUUID()
		}
		if err := tx.Create(bill).Error; err != nil {
			return fmt.Errorf("failed to create bill: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, bill.FamilyID, "bill", bill.ID, "CREATE", bill.Version, bill)
	})
}

// GetBillByID retrieves a bill by its ID.
func (r *FinanceRepo) GetBillByID(ctx context.Context, id string) (*model.FinanceBill, error) {
	var bill model.FinanceBill
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&bill)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get bill: %w", result.Error)
	}
	return &bill, nil
}

// ListBillsByFamily retrieves all bills for a family with optional status filter.
func (r *FinanceRepo) ListBillsByFamily(ctx context.Context, familyID string, status *string) ([]model.FinanceBill, error) {
	query := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	if status != nil && *status != "" {
		query = query.Where("status = ?", *status)
	}

	query = query.Order("due_at ASC")

	var bills []model.FinanceBill
	result := query.Find(&bills)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list bills: %w", result.Error)
	}
	return bills, nil
}

// MarkAsPaid marks a bill as paid and increments version.
//
// 结清同时撤销它的到期注册（PRD 卷首第 3 条的反向半边）：账单行、finance.due.revoked 的 outbox 行、
// 同步底座的 change log 共用一个事务，任一步失败整体回滚（§3.4.6、PRD 3.4「发布前落盘」）。没有这一步，
// 已付账单的到期行会永久留在首页 B 区与到期中心里 —— homeos_0008:43-44 写明 finance.due.revoked 是
// homeos_due_registration 唯一的删除路径（软删 deleted_at，不是删行），而撤销只能由持有账单的这边发出。
func (r *FinanceRepo) MarkAsPaid(ctx context.Context, id string) (*model.FinanceBill, error) {
	var updatedBill *model.FinanceBill
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bill model.FinanceBill
		result := tx.Where("id = ?", id).First(&bill)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("bill not found")
			}
			return fmt.Errorf("failed to get bill: %w", result.Error)
		}

		now := time.Now()
		bill.Status = "paid"
		bill.PaidAt = &now
		bill.Version++

		result = tx.Save(&bill)
		if result.Error != nil {
			return fmt.Errorf("failed to mark bill as paid: %w", result.Error)
		}

		if err := writeDueRevokedEvent(ctx, tx, &bill, now, revokedReasonCompleted); err != nil {
			return err
		}

		updatedBill = &bill
		return r.sync.AppendChangeLog(ctx, tx, bill.FamilyID, "bill", bill.ID, "UPDATE", bill.Version, bill)
	})

	if err != nil {
		return nil, err
	}

	return updatedBill, nil
}

// writeDueRevokedEvent writes one finance.due.revoked row into finance_outbox on the caller's
// transaction handle `tx`, so the business write and the event commit together or not at all.
//
// The envelope is the底座's own type (bus.Envelope) marshalled by bus.MarshalEnvelope: the payload
// therefore sits under the "payload" key (packages/bus/interface.go:53 `json:"payload"`), which is
// what the consuming half reads -- bus.DurableConsumer unmarshals into bus.Envelope and
// svc-homeos's handlers refuse an empty Payload (due_registered_handler.go's ErrNoPayload). A
// hand-written map with the contract fields at the TOP level (the shape bill.go's registration still
// emits, and the defect ENV-1 is fixing there) would be rejected as 缺 payload here, so this producer
// does not repeat it.
func writeDueRevokedEvent(ctx context.Context, tx *gorm.DB, bill *model.FinanceBill, revokedAt time.Time, reason string) error {
	if bill == nil {
		return errors.New("finance repo: bill is nil, cannot revoke its due registration")
	}
	if tx == nil {
		return errors.New("finance repo: no transaction handle for the due revocation outbox write")
	}

	// due_at is a timestamptz instant on both ends of the wire (homeos_0008:40) and the consumer parses
	// these stamps with time.RFC3339, so both the business_id suffix and revoked_at render in UTC.
	dueAt := bill.DueAt.UTC().Format(time.RFC3339)

	// business_id per contracts/events/finance.yaml:95「"{source_id}:{due_at}"」 -- the same anchor the
	// registration used, so a bill re-dated after being paid and re-dated again revokes under a key of
	// its own instead of being swallowed as a duplicate of the earlier撤销.
	envelope := bus.Envelope{
		EventType:  subjectDueRevoked,
		BusinessID: fmt.Sprintf("%s:%s", bill.ID, dueAt),
		FamilyID:   bill.FamilyID,
		Version:    dueEventSchemaVersion,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			// ---- 契约 payload_schema（finance.yaml:100-105，FROZEN v1.0.0），逐字段、无多余键 ----
			"source_system": financeDomainCode,                    //   source_system: finance
			"source_id":     bill.ID,                              //   source_id: uuid
			"family_id":     bill.FamilyID,                        //   family_id: uuid
			"revoked_at":    revokedAt.UTC().Format(time.RFC3339), // revoked_at: timestamp
			"reason":        reason,                               //   reason: enum(completed|deleted|expired)
		},
	}

	raw, err := bus.MarshalEnvelope(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal %s envelope: %w", subjectDueRevoked, err)
	}

	// The one outbox INSERT implementation in this repository is packages/bus's writer: it owns the
	// {code}_outbox table name and finance_0002's seven-column set with status='pending' (§3.3
	// 「INSERT finance_outbox(subject, envelope, status=pending)」, §10.3「指标最小集」).
	if err := bus.InsertOutboxMessageWithFamily(tx.WithContext(ctx), financeDomainCode,
		bill.FamilyID, subjectDueRevoked, string(raw)); err != nil {
		return fmt.Errorf("failed to insert outbox message for due revocation: %w", err)
	}

	return nil
}

// ==================== Loan Operations ====================

// CreateLoan creates a new finance loan.
func (r *FinanceRepo) CreateLoan(ctx context.Context, loan *model.FinanceLoan) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if loan.ID == "" {
			loan.ID = generateUUID()
		}
		if err := tx.Create(loan).Error; err != nil {
			return fmt.Errorf("failed to create loan: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, loan.FamilyID, "loan", loan.ID, "CREATE", loan.Version, loan)
	})
}

// GetLoanByID retrieves a loan by its ID.
func (r *FinanceRepo) GetLoanByID(ctx context.Context, id string) (*model.FinanceLoan, error) {
	var loan model.FinanceLoan
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&loan)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get loan: %w", result.Error)
	}
	return &loan, nil
}

// ListLoansByFamily retrieves all loans for a family with optional status filter.
func (r *FinanceRepo) ListLoansByFamily(ctx context.Context, familyID string, status *string) ([]model.FinanceLoan, error) {
	query := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	if status != nil && *status != "" {
		query = query.Where("status = ?", *status)
	}

	query = query.Order("created_at DESC")

	var loans []model.FinanceLoan
	result := query.Find(&loans)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list loans: %w", result.Error)
	}
	return loans, nil
}

// PayOffLoan marks a loan as paid off and increments version.
func (r *FinanceRepo) PayOffLoan(ctx context.Context, id string) (*model.FinanceLoan, error) {
	var updatedLoan *model.FinanceLoan
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var loan model.FinanceLoan
		result := tx.Where("id = ?", id).First(&loan)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("loan not found")
			}
			return fmt.Errorf("failed to get loan: %w", result.Error)
		}

		loan.Status = "paid_off"
		loan.Version++

		result = tx.Save(&loan)
		if result.Error != nil {
			return fmt.Errorf("failed to pay off loan: %w", result.Error)
		}

		updatedLoan = &loan
		return r.sync.AppendChangeLog(ctx, tx, loan.FamilyID, "loan", loan.ID, "UPDATE", loan.Version, loan)
	})

	if err != nil {
		return nil, err
	}

	return updatedLoan, nil
}

// ==================== RepaymentPlan Operations ====================

// CreateRepaymentPlan creates a new repayment plan entry.
func (r *FinanceRepo) CreateRepaymentPlan(ctx context.Context, plan *model.FinanceRepaymentPlan) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if plan.ID == "" {
			plan.ID = generateUUID()
		}
		if err := tx.Create(plan).Error; err != nil {
			return fmt.Errorf("failed to create repayment plan: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, plan.FamilyID, "repayment_plan", plan.ID, "CREATE", plan.Version, plan)
	})
}

// GetRepaymentPlanByID retrieves a repayment plan by its ID.
func (r *FinanceRepo) GetRepaymentPlanByID(ctx context.Context, id string) (*model.FinanceRepaymentPlan, error) {
	var plan model.FinanceRepaymentPlan
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&plan)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get repayment plan: %w", result.Error)
	}
	return &plan, nil
}

// GetRepaymentPlansByLoanID retrieves all repayment plans for a loan.
func (r *FinanceRepo) GetRepaymentPlansByLoanID(ctx context.Context, loanID string) ([]model.FinanceRepaymentPlan, error) {
	var plans []model.FinanceRepaymentPlan
	result := r.db.WithContext(ctx).
		Where("loan_id = ? AND deleted_at IS NULL", loanID).
		Order("due_at ASC").
		Find(&plans)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get repayment plans: %w", result.Error)
	}
	return plans, nil
}

// ListOverdueRepaymentPlans retrieves all overdue repayment plans.
func (r *FinanceRepo) ListOverdueRepaymentPlans(ctx context.Context) ([]model.FinanceRepaymentPlan, error) {
	now := time.Now()
	var plans []model.FinanceRepaymentPlan
	result := r.db.WithContext(ctx).
		Where("status = 'pending' AND due_at < ? AND deleted_at IS NULL", now).
		Order("due_at ASC").
		Find(&plans)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list overdue repayment plans: %w", result.Error)
	}
	return plans, nil
}

// MarkRepaymentPlanAsPaid marks a repayment plan as paid and increments version.
func (r *FinanceRepo) MarkRepaymentPlanAsPaid(ctx context.Context, id string) (*model.FinanceRepaymentPlan, error) {
	var updatedPlan *model.FinanceRepaymentPlan
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.FinanceRepaymentPlan
		result := tx.Where("id = ?", id).First(&plan)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("repayment plan not found")
			}
			return fmt.Errorf("failed to get repayment plan: %w", result.Error)
		}

		now := time.Now()
		plan.Status = "paid"
		plan.PaidAt = &now
		plan.Version++

		result = tx.Save(&plan)
		if result.Error != nil {
			return fmt.Errorf("failed to mark repayment plan as paid: %w", result.Error)
		}

		updatedPlan = &plan
		return r.sync.AppendChangeLog(ctx, tx, plan.FamilyID, "repayment_plan", plan.ID, "UPDATE", plan.Version, plan)
	})

	if err != nil {
		return nil, err
	}

	return updatedPlan, nil
}

// ==================== Goal Operations ====================

// CreateGoal creates a new finance goal.
func (r *FinanceRepo) CreateGoal(ctx context.Context, goal *model.FinanceGoal) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if goal.ID == "" {
			goal.ID = generateUUID()
		}
		if err := tx.Create(goal).Error; err != nil {
			return fmt.Errorf("failed to create goal: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, goal.FamilyID, "goal", goal.ID, "CREATE", goal.Version, goal)
	})
}

// GetGoalByID retrieves a goal by its ID.
func (r *FinanceRepo) GetGoalByID(ctx context.Context, id string) (*model.FinanceGoal, error) {
	var goal model.FinanceGoal
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&goal)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get goal: %w", result.Error)
	}
	return &goal, nil
}

// ListGoalsByFamily retrieves all goals for a family.
func (r *FinanceRepo) ListGoalsByFamily(ctx context.Context, familyID string) ([]model.FinanceGoal, error) {
	var goals []model.FinanceGoal
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		Order("deadline ASC, created_at DESC").
		Find(&goals)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list goals: %w", result.Error)
	}
	return goals, nil
}

// UpdateGoalProgress updates the current amount of a goal and checks if achieved.
func (r *FinanceRepo) UpdateGoalProgress(ctx context.Context, id string, currentAmountCents int64) (*model.FinanceGoal, error) {
	var updatedGoal *model.FinanceGoal
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var goal model.FinanceGoal
		result := tx.Where("id = ?", id).First(&goal)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("goal not found")
			}
			return fmt.Errorf("failed to get goal: %w", result.Error)
		}

		goal.CurrentAmountCents = currentAmountCents
		if currentAmountCents >= goal.TargetAmountCents {
			goal.IsAchieved = true
		}
		goal.Version++

		result = tx.Save(&goal)
		if result.Error != nil {
			return fmt.Errorf("failed to update goal progress: %w", result.Error)
		}

		updatedGoal = &goal
		return r.sync.AppendChangeLog(ctx, tx, goal.FamilyID, "goal", goal.ID, "UPDATE", goal.Version, goal)
	})

	if err != nil {
		return nil, err
	}

	return updatedGoal, nil
}

// ==================== Split Settlement Operations ====================

var (
	ErrInvalidSplitStatusTransition = errors.New("invalid split settlement status transition")
	ErrSplitAmountMismatch          = errors.New("split settlement amount mismatch: sum of participant amounts must equal total amount")
)

// CreateSplitSettlement creates a new split settlement record.
func (r *FinanceRepo) CreateSplitSettlement(ctx context.Context, settlement *model.FinanceSplitSettlement) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if settlement.ID == "" {
			settlement.ID = generateUUID()
		}
		if err := tx.Create(settlement).Error; err != nil {
			return fmt.Errorf("failed to create split settlement: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, settlement.FamilyID, "split_settlement", settlement.ID, "CREATE", settlement.Version, settlement)
	})
}

// GetSplitSettlementByID retrieves a split settlement by its ID.
func (r *FinanceRepo) GetSplitSettlementByID(ctx context.Context, id string) (*model.FinanceSplitSettlement, error) {
	var settlement model.FinanceSplitSettlement
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&settlement)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get split settlement: %w", result.Error)
	}
	return &settlement, nil
}

// ListSplitSettlementsByFamily retrieves all split settlements for a family with optional status filter.
func (r *FinanceRepo) ListSplitSettlementsByFamily(ctx context.Context, familyID string, status *string) ([]model.FinanceSplitSettlement, error) {
	query := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	if status != nil && *status != "" {
		query = query.Where("status = ?", *status)
	}

	query = query.Order("created_at DESC")

	var settlements []model.FinanceSplitSettlement
	result := query.Find(&settlements)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list split settlements: %w", result.Error)
	}
	return settlements, nil
}

// AddParticipant adds a participant to a split settlement.
func (r *FinanceRepo) AddParticipant(ctx context.Context, participant *model.FinanceParticipant) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Verify settlement exists and is in draft status
		var settlement model.FinanceSplitSettlement
		result := tx.Where("id = ?", participant.SettlementID).First(&settlement)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("split settlement not found")
			}
			return fmt.Errorf("failed to get split settlement: %w", result.Error)
		}

		if settlement.Status != "draft" {
			return ErrInvalidSplitStatusTransition
		}

		if participant.ID == "" {
			participant.ID = generateUUID()
		}
		if err := tx.Create(participant).Error; err != nil {
			return fmt.Errorf("failed to add participant: %w", err)
		}

		return r.sync.AppendChangeLog(ctx, tx, settlement.FamilyID, "participant", participant.ID, "CREATE", participant.Version, participant)
	})
}

// SettleSplit completes a split settlement by transitioning from pending to settled.
// Validates that sum of participant amounts equals total amount.
func (r *FinanceRepo) SettleSplit(ctx context.Context, id string) (*model.FinanceSplitSettlement, error) {
	var updatedSettlement *model.FinanceSplitSettlement
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var settlement model.FinanceSplitSettlement
		result := tx.Where("id = ?", id).First(&settlement)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("split settlement not found")
			}
			return fmt.Errorf("failed to get split settlement: %w", result.Error)
		}

		// Validate status transition: draft → pending → settled
		if settlement.Status == "settled" {
			return ErrInvalidSplitStatusTransition
		}

		// If transitioning to settled, validate amounts
		if settlement.Status == "pending" {
			// Calculate sum of participant amounts
			var totalParticipantAmount int64
			err := tx.Model(&model.FinanceParticipant{}).
				Select("COALESCE(SUM(share_amount_cents), 0)").
				Where("settlement_id = ? AND deleted_at IS NULL", id).
				Scan(&totalParticipantAmount).Error
			if err != nil {
				return fmt.Errorf("failed to calculate participant total: %w", err)
			}

			// AA zero-error validation: sum must equal total amount
			if totalParticipantAmount != settlement.TotalAmountCents {
				return fmt.Errorf("%w: expected %d, got %d", ErrSplitAmountMismatch, settlement.TotalAmountCents, totalParticipantAmount)
			}

			now := time.Now()
			settlement.Status = "settled"
			settlement.SettledAt = &now
		} else if settlement.Status == "draft" {
			settlement.Status = "pending"
		}

		settlement.Version++

		result = tx.Save(&settlement)
		if result.Error != nil {
			return fmt.Errorf("failed to settle split: %w", result.Error)
		}

		updatedSettlement = &settlement
		return r.sync.AppendChangeLog(ctx, tx, settlement.FamilyID, "split_settlement", settlement.ID, "UPDATE", settlement.Version, settlement)
	})

	if err != nil {
		return nil, err
	}

	return updatedSettlement, nil
}

// GetParticipantsBySettlement retrieves all participants for a split settlement.
func (r *FinanceRepo) GetParticipantsBySettlement(ctx context.Context, settlementID string) ([]model.FinanceParticipant, error) {
	var participants []model.FinanceParticipant
	result := r.db.WithContext(ctx).
		Where("settlement_id = ? AND deleted_at IS NULL", settlementID).
		Order("created_at ASC").
		Find(&participants)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get participants: %w", result.Error)
	}
	return participants, nil
}

// ==================== Credit Card Operations ====================

// CreateCreditCard creates a new credit card account.
func (r *FinanceRepo) CreateCreditCard(ctx context.Context, card *model.FinanceCreditCard) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if card.ID == "" {
			card.ID = generateUUID()
		}
		if err := tx.Create(card).Error; err != nil {
			return fmt.Errorf("failed to create credit card: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, card.FamilyID, "credit_card", card.ID, "CREATE", card.Version, card)
	})
}

// GetCreditCardByID retrieves a credit card by its ID.
func (r *FinanceRepo) GetCreditCardByID(ctx context.Context, id string) (*model.FinanceCreditCard, error) {
	var card model.FinanceCreditCard
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&card)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get credit card: %w", result.Error)
	}
	return &card, nil
}

// ListCreditCardsByFamily retrieves all credit cards for a family with optional status filter.
func (r *FinanceRepo) ListCreditCardsByFamily(ctx context.Context, familyID string, status *string) ([]model.FinanceCreditCard, error) {
	query := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	if status != nil && *status != "" {
		query = query.Where("status = ?", *status)
	}

	query = query.Order("created_at DESC")

	var cards []model.FinanceCreditCard
	result := query.Find(&cards)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list credit cards: %w", result.Error)
	}
	return cards, nil
}

// UpdateCreditCardBalance updates the current balance of a credit card.
// Balance is recalculated as SUM(unpaid bills).
func (r *FinanceRepo) UpdateCreditCardBalance(ctx context.Context, id string, newBalanceCents int64) (*model.FinanceCreditCard, error) {
	var updatedCard *model.FinanceCreditCard
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var card model.FinanceCreditCard
		result := tx.Where("id = ?", id).First(&card)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("credit card not found")
			}
			return fmt.Errorf("failed to get credit card: %w", result.Error)
		}

		card.CurrentBalanceCents = newBalanceCents
		card.Version++

		result = tx.Save(&card)
		if result.Error != nil {
			return fmt.Errorf("failed to update credit card balance: %w", result.Error)
		}

		updatedCard = &card
		return r.sync.AppendChangeLog(ctx, tx, card.FamilyID, "credit_card", card.ID, "UPDATE", card.Version, card)
	})

	if err != nil {
		return nil, err
	}

	return updatedCard, nil
}

// ==================== Invoice Operations ====================

var (
	ErrInvalidInvoiceStatusTransition = errors.New("invalid invoice reimbursement status transition: can only transition from pending")
)

// CreateInvoice creates a new invoice record.
func (r *FinanceRepo) CreateInvoice(ctx context.Context, invoice *model.FinanceInvoice) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if invoice.ID == "" {
			invoice.ID = generateUUID()
		}
		if err := tx.Create(invoice).Error; err != nil {
			return fmt.Errorf("failed to create invoice: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, invoice.FamilyID, "invoice", invoice.ID, "CREATE", invoice.Version, invoice)
	})
}

// GetInvoiceByID retrieves an invoice by its ID.
func (r *FinanceRepo) GetInvoiceByID(ctx context.Context, id string) (*model.FinanceInvoice, error) {
	var invoice model.FinanceInvoice
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&invoice)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get invoice: %w", result.Error)
	}
	return &invoice, nil
}

// ListInvoicesByFamily retrieves all invoices for a family with optional status filter.
func (r *FinanceRepo) ListInvoicesByFamily(ctx context.Context, familyID string, status *string) ([]model.FinanceInvoice, error) {
	query := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	if status != nil && *status != "" {
		query = query.Where("reimbursement_status = ?", *status)
	}

	query = query.Order("issue_date DESC")

	var invoices []model.FinanceInvoice
	result := query.Find(&invoices)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list invoices: %w", result.Error)
	}
	return invoices, nil
}

// MarkInvoiceAsReimbursed marks an invoice as reimbursed or rejected.
// Can only transition from pending status.
func (r *FinanceRepo) MarkInvoiceAsReimbursed(ctx context.Context, id string, status string, reason *string, transactionID *string) (*model.FinanceInvoice, error) {
	var updatedInvoice *model.FinanceInvoice
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var invoice model.FinanceInvoice
		result := tx.Where("id = ?", id).First(&invoice)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("invoice not found")
			}
			return fmt.Errorf("failed to get invoice: %w", result.Error)
		}

		// Can only transition from pending
		if invoice.ReimbursementStatus != "pending" {
			return ErrInvalidInvoiceStatusTransition
		}

		// Validate target status
		if status != "reimbursed" && status != "rejected" {
			return errors.New("invalid target status: must be 'reimbursed' or 'rejected'")
		}

		now := time.Now()
		invoice.ReimbursementStatus = status
		if status == "reimbursed" {
			invoice.ReimbursedAt = &now
			invoice.TransactionID = transactionID
		} else if status == "rejected" {
			invoice.RejectedReason = reason
		}
		invoice.Version++

		result = tx.Save(&invoice)
		if result.Error != nil {
			return fmt.Errorf("failed to update invoice status: %w", result.Error)
		}

		updatedInvoice = &invoice
		return r.sync.AppendChangeLog(ctx, tx, invoice.FamilyID, "invoice", invoice.ID, "UPDATE", invoice.Version, invoice)
	})

	if err != nil {
		return nil, err
	}

	return updatedInvoice, nil
}

// ==================== Asset-Liability Report Operations ====================

var (
	ErrAssetLiabilityMismatch = errors.New("asset-liability mismatch: net_worth must equal assets - liabilities")
)

// GenerateAssetLiabilityReport generates an asset-liability report for a family and period.
// total_assets = SUM(all account balances + savings goal current amounts)
// total_liabilities = SUM(loan principals + credit card balances)
// net_worth = assets - liabilities
func (r *FinanceRepo) GenerateAssetLiabilityReport(ctx context.Context, familyID, period string) (*model.FinanceAssetLiabilityReport, error) {
	var report *model.FinanceAssetLiabilityReport
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Calculate total assets: SUM(account balances) + SUM(goal current amounts)
		var totalAccountBalance int64
		err := tx.Model(&model.FinanceAccount{}).
			Select("COALESCE(SUM(balance), 0)").
			Where("family_id = ? AND deleted_at IS NULL", familyID).
			Scan(&totalAccountBalance).Error
		if err != nil {
			return fmt.Errorf("failed to calculate account balances: %w", err)
		}

		var totalGoalAmount int64
		err = tx.Model(&model.FinanceGoal{}).
			Select("COALESCE(SUM(current_amount_cents), 0)").
			Where("family_id = ? AND deleted_at IS NULL", familyID).
			Scan(&totalGoalAmount).Error
		if err != nil {
			return fmt.Errorf("failed to calculate goal amounts: %w", err)
		}

		totalAssets := totalAccountBalance + totalGoalAmount

		// Calculate total liabilities: SUM(loan principals where status='active') + SUM(credit card balances)
		var totalLoanPrincipal int64
		err = tx.Model(&model.FinanceLoan{}).
			Select("COALESCE(SUM(principal_cents), 0)").
			Where("family_id = ? AND status = 'active' AND deleted_at IS NULL", familyID).
			Scan(&totalLoanPrincipal).Error
		if err != nil {
			return fmt.Errorf("failed to calculate loan principals: %w", err)
		}

		var totalCreditCardBalance int64
		err = tx.Model(&model.FinanceCreditCard{}).
			Select("COALESCE(SUM(current_balance_cents), 0)").
			Where("family_id = ? AND deleted_at IS NULL", familyID).
			Scan(&totalCreditCardBalance).Error
		if err != nil {
			return fmt.Errorf("failed to calculate credit card balances: %w", err)
		}

		totalLiabilities := totalLoanPrincipal + totalCreditCardBalance

		// Calculate net worth
		netWorth := totalAssets - totalLiabilities

		// Validate: net_worth must equal assets - liabilities
		if netWorth != totalAssets-totalLiabilities {
			return ErrAssetLiabilityMismatch
		}

		// Create or update report for this period
		var existingReport model.FinanceAssetLiabilityReport
		result := tx.Where("family_id = ? AND period = ? AND deleted_at IS NULL", familyID, period).First(&existingReport)

		now := time.Now()
		newReport := &model.FinanceAssetLiabilityReport{
			FamilyID:              familyID,
			Period:                period,
			TotalAssetsCents:      totalAssets,
			TotalLiabilitiesCents: totalLiabilities,
			NetWorthCents:         netWorth,
			SnapshotAt:            now,
			Version:               1,
		}

		if result.Error == nil {
			// Update existing report
			existingReport.TotalAssetsCents = totalAssets
			existingReport.TotalLiabilitiesCents = totalLiabilities
			existingReport.NetWorthCents = netWorth
			existingReport.SnapshotAt = now
			existingReport.Version++

			if err := tx.Save(&existingReport).Error; err != nil {
				return fmt.Errorf("failed to update asset-liability report: %w", err)
			}
			report = &existingReport
		} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Create new report
			if newReport.ID == "" {
				newReport.ID = generateUUID()
			}
			if err := tx.Create(newReport).Error; err != nil {
				return fmt.Errorf("failed to create asset-liability report: %w", err)
			}
			report = newReport
		} else {
			return fmt.Errorf("failed to query existing report: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, familyID, "asset_liability_report", report.ID, "CREATE", report.Version, report)
	})

	if err != nil {
		return nil, err
	}

	return report, nil
}

// GetLatestAssetLiabilityReport retrieves the latest asset-liability report for a family.
func (r *FinanceRepo) GetLatestAssetLiabilityReport(ctx context.Context, familyID string) (*model.FinanceAssetLiabilityReport, error) {
	var report model.FinanceAssetLiabilityReport
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		Order("snapshot_at DESC").
		First(&report)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get latest asset-liability report: %w", result.Error)
	}
	return &report, nil
}

// GetAssetLiabilityReportByPeriod retrieves an asset-liability report for a specific period.
func (r *FinanceRepo) GetAssetLiabilityReportByPeriod(ctx context.Context, familyID, period string) (*model.FinanceAssetLiabilityReport, error) {
	var report model.FinanceAssetLiabilityReport
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND period = ? AND deleted_at IS NULL", familyID, period).
		First(&report)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get asset-liability report: %w", result.Error)
	}
	return &report, nil
}

// AddParticipantDirectly adds a participant without status validation (for testing purposes).
// This bypasses the draft-only check to allow test setup.
func (r *FinanceRepo) AddParticipantDirectly(ctx context.Context, participant *model.FinanceParticipant) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if participant.ID == "" {
			participant.ID = generateUUID()
		}
		if err := tx.Create(participant).Error; err != nil {
			return fmt.Errorf("failed to add participant: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, "test-family-001", "participant", participant.ID, "CREATE", participant.Version, participant)
	})
}

// ==================== Tag Operations ====================

// CreateTag creates a new finance tag.
func (r *FinanceRepo) CreateTag(ctx context.Context, tag *model.FinanceTag) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tag.ID == "" {
			tag.ID = generateUUID()
		}
		if err := tx.Create(tag).Error; err != nil {
			return fmt.Errorf("failed to create tag: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, tag.FamilyID, "tag", tag.ID, "CREATE", 1, tag)
	})
}

// GetTagByID retrieves a tag by its ID.
func (r *FinanceRepo) GetTagByID(ctx context.Context, familyID string, tagID string) (*model.FinanceTag, error) {
	var tag model.FinanceTag
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", tagID, familyID).First(&tag)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get tag: %w", result.Error)
	}
	return &tag, nil
}

// ListTagsByFamily retrieves all tags for a family.
func (r *FinanceRepo) ListTagsByFamily(ctx context.Context, familyID string) ([]model.FinanceTag, error) {
	var tags []model.FinanceTag
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		Order("created_at DESC").
		Find(&tags)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list tags: %w", result.Error)
	}
	return tags, nil
}

// UpdateTag updates a tag.
func (r *FinanceRepo) UpdateTag(ctx context.Context, tag *model.FinanceTag) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Save(tag)
		if result.Error != nil {
			return fmt.Errorf("failed to update tag: %w", result.Error)
		}
		return r.sync.AppendChangeLog(ctx, tx, tag.FamilyID, "tag", tag.ID, "UPDATE", 1, tag)
	})
}

// DeleteTag soft-deletes a tag.
func (r *FinanceRepo) DeleteTag(ctx context.Context, familyID string, tagID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tag model.FinanceTag
		result := tx.Where("id = ? AND family_id = ?", tagID, familyID).First(&tag)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("tag not found")
			}
			return fmt.Errorf("failed to get tag: %w", result.Error)
		}

		result = tx.Where("id = ? AND family_id = ?", tagID, familyID).Delete(&tag)
		if result.Error != nil {
			return fmt.Errorf("failed to delete tag: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, familyID, "tag", tagID, "DELETE", 1, tag)
	})
}

// ==================== Recurring Rule Operations ====================

// CreateRecurringRule creates a new recurring rule.
func (r *FinanceRepo) CreateRecurringRule(ctx context.Context, rule *model.FinanceRecurringRule) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if rule.ID == "" {
			rule.ID = generateUUID()
		}
		if err := tx.Create(rule).Error; err != nil {
			return fmt.Errorf("failed to create recurring rule: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, rule.FamilyID, "recurring_rule", rule.ID, "CREATE", 1, rule)
	})
}

// GetRecurringRuleByID retrieves a recurring rule by its ID.
func (r *FinanceRepo) GetRecurringRuleByID(ctx context.Context, familyID string, ruleID string) (*model.FinanceRecurringRule, error) {
	var rule model.FinanceRecurringRule
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", ruleID, familyID).First(&rule)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get recurring rule: %w", result.Error)
	}
	return &rule, nil
}

// ListRecurringRulesByFamily retrieves all recurring rules for a family.
func (r *FinanceRepo) ListRecurringRulesByFamily(ctx context.Context, familyID string) ([]model.FinanceRecurringRule, error) {
	var rules []model.FinanceRecurringRule
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		Order("next_execute_at ASC").
		Find(&rules)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list recurring rules: %w", result.Error)
	}
	return rules, nil
}

// ListDueRecurringRules retrieves all recurring rules that are due for execution.
func (r *FinanceRepo) ListDueRecurringRules(ctx context.Context, now time.Time) ([]model.FinanceRecurringRule, error) {
	var rules []model.FinanceRecurringRule
	result := r.db.WithContext(ctx).
		Where("is_active = true AND next_execute_at <= ? AND deleted_at IS NULL", now).
		Order("next_execute_at ASC").
		Find(&rules)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list due recurring rules: %w", result.Error)
	}
	return rules, nil
}

// UpdateRecurringRule updates a recurring rule.
func (r *FinanceRepo) UpdateRecurringRule(ctx context.Context, rule *model.FinanceRecurringRule) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Save(rule)
		if result.Error != nil {
			return fmt.Errorf("failed to update recurring rule: %w", result.Error)
		}
		return r.sync.AppendChangeLog(ctx, tx, rule.FamilyID, "recurring_rule", rule.ID, "UPDATE", 1, rule)
	})
}

// MarkRecurringRuleExecuted marks a rule as executed and calculates the next execution time.
func (r *FinanceRepo) MarkRecurringRuleExecuted(ctx context.Context, ruleID string, nextExecuteAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&model.FinanceRecurringRule{}).
			Where("id = ?", ruleID).
			Updates(map[string]any{
				"last_executed_at": now,
				"next_execute_at":  nextExecuteAt,
				"updated_at":       now,
			})
		if result.Error != nil {
			return fmt.Errorf("failed to mark recurring rule as executed: %w", result.Error)
		}
		return nil
	})
}

// ==================== Budget Period Operations ====================

// CreateBudgetPeriod creates a new budget period.
func (r *FinanceRepo) CreateBudgetPeriod(ctx context.Context, period *model.FinanceBudgetPeriod) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if period.ID == "" {
			period.ID = generateUUID()
		}
		if err := tx.Create(period).Error; err != nil {
			return fmt.Errorf("failed to create budget period: %w", err)
		}
		return r.sync.AppendChangeLog(ctx, tx, period.FamilyID, "budget_period", period.ID, "CREATE", 1, period)
	})
}

// GetBudgetPeriodByID retrieves a budget period by its ID.
func (r *FinanceRepo) GetBudgetPeriodByID(ctx context.Context, familyID string, periodID string) (*model.FinanceBudgetPeriod, error) {
	var period model.FinanceBudgetPeriod
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", periodID, familyID).First(&period)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get budget period: %w", result.Error)
	}
	return &period, nil
}

// ListBudgetPeriodsByFamily retrieves all budget periods for a family.
func (r *FinanceRepo) ListBudgetPeriodsByFamily(ctx context.Context, familyID string) ([]model.FinanceBudgetPeriod, error) {
	var periods []model.FinanceBudgetPeriod
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		Order("start_date DESC").
		Find(&periods)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list budget periods: %w", result.Error)
	}
	return periods, nil
}

// UpdateBudgetPeriod updates a budget period.
func (r *FinanceRepo) UpdateBudgetPeriod(ctx context.Context, period *model.FinanceBudgetPeriod) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Save(period)
		if result.Error != nil {
			return fmt.Errorf("failed to update budget period: %w", result.Error)
		}
		return r.sync.AppendChangeLog(ctx, tx, period.FamilyID, "budget_period", period.ID, "UPDATE", 1, period)
	})
}

// DeleteBudgetPeriod soft-deletes a budget period.
func (r *FinanceRepo) DeleteBudgetPeriod(ctx context.Context, familyID string, periodID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var period model.FinanceBudgetPeriod
		result := tx.Where("id = ? AND family_id = ?", periodID, familyID).First(&period)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return errors.New("budget period not found")
			}
			return fmt.Errorf("failed to get budget period: %w", result.Error)
		}

		result = tx.Where("id = ? AND family_id = ?", periodID, familyID).Delete(&period)
		if result.Error != nil {
			return fmt.Errorf("failed to delete budget period: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, familyID, "budget_period", periodID, "DELETE", 1, period)
	})
}
