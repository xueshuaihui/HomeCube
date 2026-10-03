// Package service provides business logic for the finance service.
package service

import (
	"context"
	"fmt"

	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
	homeosmodel "github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"gorm.io/gorm"
)

// SyncTransactionToSearchIndex syncs a finance transaction to the global search index.
// Per PRD 14.5 #7: search index is maintained by svc-homeos, fed by events from various domains.
// This function is called when a transaction is created/updated to keep the search index in sync.
func SyncTransactionToSearchIndex(ctx context.Context, db *gorm.DB, tx *model.FinanceTransaction) error {
	// Concatenate key fields for searchable content
	// Include description and category name (if available)
	content := tx.Description

	// Note: In a real implementation, you would fetch the category name here
	// For now, we just use the description as the searchable content
	// TODO: Join with finance_category to get category name

	index := &homeosmodel.HomeosSearchIndex{
		FamilyID: tx.FamilyID,
		Domain:   "finance",
		Entity:   "transaction",
		EntityID: tx.ID,
		Content:  content,
	}

	return repo.UpsertSearchIndex(ctx, db, index)
}

// RemoveTransactionFromSearchIndex removes a finance transaction from the search index.
// Called when a transaction is deleted.
func RemoveTransactionFromSearchIndex(ctx context.Context, db *gorm.DB, familyID string, transactionID string) error {
	return repo.DeleteSearchIndex(ctx, db, "finance", transactionID)
}

// SyncAccountToSearchIndex syncs a finance account to the global search index.
func SyncAccountToSearchIndex(ctx context.Context, db *gorm.DB, account *model.FinanceAccount) error {
	content := fmt.Sprintf("%s %s", account.Name, account.Type)

	index := &homeosmodel.HomeosSearchIndex{
		FamilyID: account.FamilyID,
		Domain:   "finance",
		Entity:   "account",
		EntityID: account.ID,
		Content:  content,
	}

	return repo.UpsertSearchIndex(ctx, db, index)
}

// RemoveAccountFromSearchIndex removes a finance account from the search index.
func RemoveAccountFromSearchIndex(ctx context.Context, db *gorm.DB, familyID string, accountID string) error {
	return repo.DeleteSearchIndex(ctx, db, "finance", accountID)
}

// SyncCategoryToSearchIndex syncs a finance category to the global search index.
func SyncCategoryToSearchIndex(ctx context.Context, db *gorm.DB, category *model.FinanceCategory) error {
	index := &homeosmodel.HomeosSearchIndex{
		FamilyID: category.FamilyID,
		Domain:   "finance",
		Entity:   "category",
		EntityID: category.ID,
		Content:  category.Name,
	}

	return repo.UpsertSearchIndex(ctx, db, index)
}

// RemoveCategoryFromSearchIndex removes a finance category from the search index.
func RemoveCategoryFromSearchIndex(ctx context.Context, db *gorm.DB, familyID string, categoryID string) error {
	return repo.DeleteSearchIndex(ctx, db, "finance", categoryID)
}
