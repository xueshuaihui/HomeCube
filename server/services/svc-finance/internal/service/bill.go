// Package service provides business logic for the finance service.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/gorm"
)

// subjectDueRegistered is the event name of contracts/events/finance.yaml's finance.due.registered
// (§3.1「subject 命名即事件名：{code}.{object}.{action}」). Declared once so the outbox row's subject
// column and the envelope's event_type key can never drift apart.
const subjectDueRegistered = "finance.due.registered"

// envelopeSchemaVersion mirrors the value the other svc-finance producer stamps (svc-finance
// internal/service/budget_alert.go) and svc-homeos' outbox writer, so all three envelopes agree.
const envelopeSchemaVersion = "1.0"

// BillService handles bill-related business logic.
type BillService struct {
	repo *repo.FinanceRepo
	db   *gorm.DB // the service's own handle: it owns the transaction that spans business write + event write
	code string   // service code for outbox table naming
}

// NewBillService creates a new bill service instance.
func NewBillService(repo *repo.FinanceRepo, db *gorm.DB, code string) *BillService {
	return &BillService{
		repo: repo,
		db:   db,
		code: code,
	}
}

// CreateBillWithDueRegistration creates a bill and registers its due date with HomeOS in ONE
// database transaction.
//
// Why one transaction (§3.4.6, PRD 3.4「发布前落盘」and this repo's 「无跨服务事务」 ruling): the
// outbox pattern only holds when the business row and the pending event row commit together. Committed
// separately, either
//   - the bill commits but the event is lost (到期中心永远看不到这笔账单), or
//   - the event commits but the bill rolled back (注册了一个不存在的到期对象).
//
// Any error from RegisterBillDue aborts the whole write, so the caller gets a failed request rather
// than a bill whose due date never got registered.
func (s *BillService) CreateBillWithDueRegistration(ctx context.Context, bill *model.FinanceBill) error {
	if s.db == nil {
		return errors.New("bill service: no database handle, cannot open the bill+outbox transaction")
	}
	if bill == nil {
		return errors.New("bill service: bill is nil")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// repo.CreateBill opens its own transaction on whichever handle it was built from; handing it a
		// repo bound to `tx` makes that an inner SAVEPOINT of this transaction (GORM nests transactions
		// that way), so the business write and the outbox row share one commit/rollback without changing
		// repo's public contract.
		if err := repo.NewFinanceRepo(tx).CreateBill(ctx, bill); err != nil {
			return fmt.Errorf("failed to create bill: %w", err)
		}

		if err := s.RegisterBillDue(ctx, tx, bill); err != nil {
			return err
		}

		return nil
	})
}

// RegisterBillDue registers a bill's due date with HomeOS via outbox event.
// This should be called when creating or updating a bill with a due date, inside the caller's
// transaction: `db` is expected to be that transaction handle (see CreateBillWithDueRegistration).
func (s *BillService) RegisterBillDue(ctx context.Context, db *gorm.DB, bill *model.FinanceBill) error {
	if bill == nil {
		return errors.New("bill service: bill is nil")
	}
	if db == nil {
		return errors.New("bill service: no transaction handle for the outbox write")
	}

	// due_at is a timestamptz instant on both sides of the wire (finance_0006「due_at TIMESTAMPTZ」,
	// homeos_0008「due_at timestamptz」), and the consumer parses it with time.RFC3339
	// (svc-homeos internal/consumer/finance_consumer.go), so it has to be rendered in UTC: the previous
	// "2006-01-02T15:04:05Z" layout printed the wall clock of the bill's own location and appended a
	// literal Z, which shifted every non-UTC deployment's due date by its offset.
	dueAt := bill.DueAt.UTC().Format(time.RFC3339)

	// business_id per contracts/README.md:29 (PRD 10.4): 「{code}.due.registered = {source_id}:{due_at}
	// （改期即重发）」-- the consumer upserts on (source_system, source_id, kind), so a changed due date
	// must produce a different key or the rescheduled bill would be swallowed as a duplicate.
	//
	// It also has to be non-empty: packages/bus/consumer.go:210-215 refuses a delivery whose dedupe key
	// (event_type, business_id) has an empty half, and that refusal NAKs without writing a dead-letter
	// row -- the message would vanish silently.
	businessID := fmt.Sprintf("%s:%s", bill.ID, dueAt)

	// The envelope is built from the base's own type (packages/bus.Envelope, packages/bus/interface.go:41)
	// and serialised by the base's own writer (bus.MarshalEnvelope), never from a hand-assembled map.
	//
	// This is the defect ENV-1 closes: the consumer decodes with json.Unmarshal into bus.Envelope
	// (packages/bus/consumer.go:151-157) and bus.Envelope carries the event data under the `payload` tag
	// (interface.go:53), so a flat envelope decoded to a nil Payload -- and
	// svc-homeos/internal/consumer/due_registered_handler.go:36 refused every such delivery with
	// ErrNoPayload, NAK-ing it into the dead letter. The producer and the consumer were each green against
	// their own shape; nothing crossed the two packages until this test existed.
	envelope := bus.Envelope{
		// ---- 信封元数据（§3.1「版本不进 subject 而进信封 version 字段」、PRD 10.4 幂等键）----
		EventType:  subjectDueRegistered,
		BusinessID: businessID,
		FamilyID:   bill.FamilyID,
		Version:    envelopeSchemaVersion,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),

		// ---- 契约 payload：contracts/events/finance.yaml finance.due.registered（FROZEN, v1.0.0）----
		// The field set below is exactly the entry's payload_schema (lines 84-91), no more and no less.
		Payload: map[string]any{
			"source_system": "finance",
			"source_id":     bill.ID,
			"family_id":     bill.FamilyID,
			"due_at":        dueAt,
			// kind 的取值是「被注册的到期对象属于哪一类」，受控枚举的权威源是
			// contracts/events/finance.yaml finance.due.registered 的 kind: enum(bill|budget|goal|repayment)（已冻结）。
			// 与 homeos_0008 迁移的 CHECK 同四值。账单的到期注册只能是 "bill"；
			// 「账单已付」是另一条事件 finance.bill.paid，不带 kind（PRD 658、1298）。
			"kind": "bill",
			// title 在注册时定稿（homeos_0008 的 title 列）；FinanceBill 没有标题列，可溯源的对象名只有 id。
			"title": fmt.Sprintf("账单到期: %s", bill.ID),
			// members: array[uuid] 是契约声明的第 7 个字段。FinanceBill 没有成员归属列
			// （finance_0006 的 finance_bill 只有 family_id/payee_id），消费方 homeos_0008 也没建该列，
			// 所以这里给「本账单没有指定到具体成员」的空集，而不是编造成员。已上报待决策。
			"members": []string{},
		},
	}

	envelopeJSON, err := bus.MarshalEnvelope(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	// Insert into the outbox table within the same transaction as the business operation, through the
	// base's writer: bus.InsertOutboxMessageWithFamily owns the {code}_outbox table name and 0002's
	// seven-column set (id, family_id, subject, envelope, status, attempts, created_at) with
	// status='pending' (§3.3「INSERT finance_outbox(subject, envelope, status=pending)」+ §10.3
	// 「指标最小集（全部带 family_id 与 code）」). There is exactly one outbox INSERT implementation in
	// the repository, and it is packages/bus.
	if err := bus.InsertOutboxMessageWithFamily(db.WithContext(ctx), s.code, bill.FamilyID,
		subjectDueRegistered, string(envelopeJSON)); err != nil {
		return fmt.Errorf("failed to insert outbox message for due registration: %w", err)
	}

	return nil
}
