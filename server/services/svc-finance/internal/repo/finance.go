// Package repo provides data access operations for the finance service.
package repo

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/sync"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"gorm.io/gorm"
)

var (
	ErrAccountBalanceNonZero = errors.New("account balance must be zero before archiving")
	ErrOptimisticLock        = errors.New("optimistic lock conflict: record was modified by another request")
	ErrDuplicateRequest      = errors.New("duplicate request: this transaction has already been processed")
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

		updatedBill = &bill
		return r.sync.AppendChangeLog(ctx, tx, bill.FamilyID, "bill", bill.ID, "UPDATE", bill.Version, bill)
	})

	if err != nil {
		return nil, err
	}

	return updatedBill, nil
}
