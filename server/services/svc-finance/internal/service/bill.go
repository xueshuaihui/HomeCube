// Package service provides business logic for the finance service.
package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/gorm"
)

// BillService handles bill-related business logic.
type BillService struct {
	repo *repo.FinanceRepo
	code string // service code for outbox table naming
}

// NewBillService creates a new bill service instance.
func NewBillService(repo *repo.FinanceRepo, code string) *BillService {
	return &BillService{
		repo: repo,
		code: code,
	}
}

// RegisterBillDue registers a bill's due date with HomeOS via outbox event.
// This should be called when creating or updating a bill with a due date.
func (s *BillService) RegisterBillDue(ctx context.Context, db *gorm.DB, bill *model.FinanceBill) error {
	// The bill data has already been written (by CreateBill or UpdateBill)
	// Now we publish the due registration event via outbox in the same transaction

	envelope := map[string]any{
		"source_system": "finance",
		"source_id":     bill.ID,
		// kind 的取值是「被注册的到期对象属于哪一类」，受控枚举的权威源是
		// contracts/events/finance.yaml finance.due.registered 的 kind: enum(bill|budget|goal|repayment)（已冻结）。
		// 与 homeos_0008 迁移的 CHECK 同四值。账单的到期注册只能是 "bill"；
		// 「账单已付」是另一条事件 finance.bill.paid，不带 kind（PRD 658、1298）。
		"kind":         "bill",
		"due_at":       bill.DueAt.Format("2006-01-02T15:04:05Z"),
		"title":        fmt.Sprintf("账单到期: %s", bill.ID), // In production, this would use bill.Title if available
		"family_id":    bill.FamilyID,
		"amount_cents": bill.AmountCents,
	}

	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	// Insert into outbox within the same transaction as the business operation
	if err := bus.InsertOutboxMessage(db, s.code, "finance.due.registered", string(envelopeJSON)); err != nil {
		return fmt.Errorf("failed to insert outbox message for due registration: %w", err)
	}

	return nil
}
