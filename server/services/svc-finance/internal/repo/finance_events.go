// finance_events.go —— svc-finance 的 8 条契约事件里由 repo 侧落 outbox 的六个发布点
// （finance.transaction.created / .updated / .deleted、finance.budget.exceeded、
// finance.bill.paid、finance.demo_item.created）。
//
// 口径全部来自 contracts/events/finance.yaml（FROZEN v1.0.0）：
//   - subject == event_type == §3.1「subject 命名即事件名：{code}.{object}.{action}」；
//   - business_id 按每个事件的 "{...}" 模板（PRD 10.4 取值约定）；
//   - payload 的键名/类型与 payload_schema 逐字一致，不发明字段、不改名。
//
// 形状照抄本仓已有的三支working producer（service/bill.go 的 due.registered、
// repo/finance.go writeDueRevokedEvent 的 due.revoked、service/recurring.go 的
// transaction.created 定时链路）：信封一律 bus.Envelope + bus.MarshalEnvelope，
// 事件数据在 payload 对象里（packages/bus/consumer.go 按 bus.Envelope 解码，
// 平铺 map 会解出 nil Payload 被消费方 NAK —— ENV-1 实测过的缺陷）；落表一律
// bus.InsertOutboxMessageWithFamily，与业务写共用**同一个事务句柄**（§3.4.6
// 「发布前落盘」、无跨服务事务）。
//
// created_by 口径（上报项）：契约的 finance.transaction.created payload 用 created_by，
// 而列名改成 member_id/is_private 由另一张卡负责（见 project memory「私密流水口径」）。
// 本文件只发布**列的当前内容**（finance_0015 的 created_by，多数行是 NULL），不改名、
// 不回填 —— 字段 mismatches 记录在验收报告，不在此擅自修。
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"gorm.io/gorm"
)

// 契约 subject 常量（contracts/events/finance.yaml 逐条 event_type；声明一次，
// outbox 行的 subject 列与信封的 event_type 永远同源 —— service/bill.go:19 同样的做法）。
const (
	subjectTransactionCreated = "finance.transaction.created"
	subjectTransactionUpdated = "finance.transaction.updated"
	subjectTransactionDeleted = "finance.transaction.deleted"
	subjectBudgetExceeded     = "finance.budget.exceeded"
	subjectBillPaid           = "finance.bill.paid"
	subjectDemoItemCreated    = "finance.demo_item.created"

	// transactionEventSchemaVersion == 契约 finance.yaml:11 的 version: "1.0"，
	// 也是本域其余 producer 在信封 version 字段上写的同一个值（§3.1「版本不进 subject 而进信封」）。
	transactionEventSchemaVersion = "1.0"
)

// writeContractEvent 是这支文件的唯一 outbox 写点：marshal（底座自己的
// bus.MarshalEnvelope）→ 落盘（底座唯一的 InsertOutboxMessageWithFamily，
// finance_0002 的七列 + status='pending'）。tx 必须是调用方业务写的同一个事务句柄。
func writeContractEvent(ctx context.Context, tx *gorm.DB, familyID string, env bus.Envelope) error {
	raw, err := bus.MarshalEnvelope(env)
	if err != nil {
		return fmt.Errorf("failed to marshal %s envelope: %w", env.EventType, err)
	}
	if err := bus.InsertOutboxMessageWithFamily(tx.WithContext(ctx), financeDomainCode,
		familyID, env.EventType, string(raw)); err != nil {
		return fmt.Errorf("failed to insert outbox message for %s: %w", env.EventType, err)
	}
	return nil
}

// transactionCreatedEnvelope 是 finance.transaction.created 的契约信封
// （payload_schema: transaction_id/family_id/type/amount_cents/category_id/account_id/
// occurred_at/created_by，business_id = {transaction_id}）。
func transactionCreatedEnvelope(t *model.FinanceTransaction) bus.Envelope {
	return bus.Envelope{
		EventType:  subjectTransactionCreated,
		BusinessID: t.ID,
		FamilyID:   t.FamilyID,
		Version:    transactionEventSchemaVersion,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"transaction_id": t.ID,
			"family_id":      t.FamilyID,
			"type":           t.Type,
			"amount_cents":   t.AmountCents,
			"category_id":    stringPtrOrNil(t.CategoryID),
			"account_id":     t.AccountID,
			"occurred_at":    t.OccurredAt.UTC().Format(time.RFC3339),
			// created_by：契约字段名。发布的是列的当前内容（finance_0015 引入，存 member_id，
			// 手工记账路径今天仍写 NULL）；见文件头「created_by 口径（上报项）」。
			"created_by": stringPtrOrNil(t.CreatedBy),
		},
	}
}

// transactionUpdatedEnvelope 是 finance.transaction.updated 的契约信封
// （payload_schema: transaction_id/family_id/updated_fields(object)/version(bigint)，
// business_id = {transaction_id}）。updated_fields 是**本次实际被改的列名 → 新值**，
// 底座版本列与 updated_at 这类记账噪音不进契约对象。
func transactionUpdatedEnvelope(t *model.FinanceTransaction, updatedFields map[string]any, version int64) bus.Envelope {
	fields := make(map[string]any, len(updatedFields))
	for k, v := range updatedFields {
		fields[k] = v
	}
	return bus.Envelope{
		EventType:  subjectTransactionUpdated,
		BusinessID: t.ID,
		FamilyID:   t.FamilyID,
		Version:    transactionEventSchemaVersion,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"transaction_id": t.ID,
			"family_id":      t.FamilyID,
			"updated_fields": fields,
			"version":        version,
		},
	}
}

// transactionDeletedEnvelope 是 finance.transaction.deleted 的契约信封
// （payload_schema: transaction_id/family_id/deleted_at/deleted_by，软删进回收站）。
func transactionDeletedEnvelope(t *model.FinanceTransaction, deletedAt time.Time, deletedBy string) bus.Envelope {
	var deletedByVal any
	if deletedBy != "" {
		deletedByVal = deletedBy
	}
	return bus.Envelope{
		EventType:  subjectTransactionDeleted,
		BusinessID: t.ID,
		FamilyID:   t.FamilyID,
		Version:    transactionEventSchemaVersion,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"transaction_id": t.ID,
			"family_id":      t.FamilyID,
			"deleted_at":     deletedAt.UTC().Format(time.RFC3339),
			"deleted_by":     deletedByVal,
		},
	}
}

// billPaidEnvelope 是 finance.bill.paid 的契约信封（payload_schema: bill_id/family_id/
// paid_at/paid_by/transaction_id，business_id = {bill_id}）。
//
// transaction_id：契约描述是「标记已付（生成流水并回写账单状态）」，但当前
// MarkAsPaid 只回写账单状态、不生成流水（FinanceBill 也没有 transaction_id 列），
// 所以这里发布 null 而不是编造一个 id —— 已上报，生成流水属账单结清的后续卡。
func billPaidEnvelope(bill *model.FinanceBill, paidAt time.Time, paidBy string) bus.Envelope {
	var paidByVal any
	if paidBy != "" {
		paidByVal = paidBy
	}
	return bus.Envelope{
		EventType:  subjectBillPaid,
		BusinessID: bill.ID,
		FamilyID:   bill.FamilyID,
		Version:    transactionEventSchemaVersion,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"bill_id":        bill.ID,
			"family_id":      bill.FamilyID,
			"paid_at":        paidAt.UTC().Format(time.RFC3339),
			"paid_by":        paidByVal,
			"transaction_id": nil,
		},
	}
}

// budgetExceededEnvelope 是 finance.budget.exceeded 的契约信封（payload_schema:
// budget_id/family_id/period/budget_amount_cents/actual_amount_cents/exceeded_at）。
//
// business_id = {budget_id}:{period}（finance.yaml:63 + PRD 10.4「可重复发生的事件必须带
// 周期后缀」）—— 同一预算可以逐月/逐季分别超支，每个周期一条事件，同周期内的重复超限
// 由消费方按这个键去重。
func budgetExceededEnvelope(budget *model.FinanceBudget, period string, actual int64, exceededAt time.Time) bus.Envelope {
	return bus.Envelope{
		EventType:  subjectBudgetExceeded,
		BusinessID: fmt.Sprintf("%s:%s", budget.ID, period),
		FamilyID:   budget.FamilyID,
		Version:    transactionEventSchemaVersion,
		Timestamp:  exceededAt.UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"budget_id":           budget.ID,
			"family_id":           budget.FamilyID,
			"period":              period,
			"budget_amount_cents": budget.AmountCents,
			"actual_amount_cents": actual,
			"exceeded_at":         exceededAt.UTC().Format(time.RFC3339),
		},
	}
}

// demoItemCreatedEnvelope 是 finance.demo_item.created 的契约信封（payload_schema:
// demo_item_id/family_id/title/due_at/created_by，business_id = {demo_item_id}）。
// P1-M1 五步验证专用，M2 收口随 demo 表一起删除。
func demoItemCreatedEnvelope(item *model.FinanceDemoItem) bus.Envelope {
	return bus.Envelope{
		EventType:  subjectDemoItemCreated,
		BusinessID: item.ID,
		FamilyID:   item.FamilyID,
		Version:    transactionEventSchemaVersion,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"demo_item_id": item.ID,
			"family_id":    item.FamilyID,
			"title":        item.Title,
			"due_at":       item.DueAt.UTC().Format(time.RFC3339),
			"created_by":   stringPtrOrNil(item.CreatedBy),
		},
	}
}

func stringPtrOrNil(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

// writeTransactionCreatedEvent / Updated / Deleted 是三支 repo 内事件写点，
// 由各自的业务事务句柄调用（CreateTransaction / UpdateTransaction / SoftDeleteTransaction）。
func writeTransactionCreatedEvent(ctx context.Context, tx *gorm.DB, t *model.FinanceTransaction) error {
	return writeContractEvent(ctx, tx, t.FamilyID, transactionCreatedEnvelope(t))
}

func writeTransactionUpdatedEvent(ctx context.Context, tx *gorm.DB, t *model.FinanceTransaction, updatedFields map[string]any, version int64) error {
	return writeContractEvent(ctx, tx, t.FamilyID, transactionUpdatedEnvelope(t, updatedFields, version))
}

func writeTransactionDeletedEvent(ctx context.Context, tx *gorm.DB, t *model.FinanceTransaction, deletedAt time.Time, deletedBy string) error {
	return writeContractEvent(ctx, tx, t.FamilyID, transactionDeletedEnvelope(t, deletedAt, deletedBy))
}

func writeBillPaidEvent(ctx context.Context, tx *gorm.DB, bill *model.FinanceBill, paidAt time.Time, paidBy string) error {
	return writeContractEvent(ctx, tx, bill.FamilyID, billPaidEnvelope(bill, paidAt, paidBy))
}

func writeDemoItemCreatedEvent(ctx context.Context, tx *gorm.DB, item *model.FinanceDemoItem) error {
	return writeContractEvent(ctx, tx, item.FamilyID, demoItemCreatedEnvelope(item))
}

// CreateDemoItem 写入一条 P1 跨服务五步验证的 demo 项，并在**同一事务**里落
// finance.demo_item.created 的 outbox 行（契约见 demoItemCreatedEnvelope）。
// 限时工件：M2 收口（S19）时随 finance_0017 的 demo 表、handler 接口一起删除。
func (r *FinanceRepo) CreateDemoItem(ctx context.Context, item *model.FinanceDemoItem) error {
	if item == nil {
		return errors.New("finance repo: demo item is nil")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if item.ID == "" {
			item.ID = generateUUID()
		}
		if item.Version == 0 {
			item.Version = 1
		}
		if err := tx.Create(item).Error; err != nil {
			return fmt.Errorf("failed to create demo item: %w", err)
		}
		if err := writeDemoItemCreatedEvent(ctx, tx, item); err != nil {
			return err
		}
		return r.sync.AppendChangeLog(ctx, tx, item.FamilyID, "demo_item", item.ID, "CREATE", item.Version, item)
	})
}

// budgetPeriodBucket 把一条流水的 occurred_at 归入预算周期（monthly/quarterly/yearly，
// 与 model.FinanceBudget.Period 的取值域一致），返回 (标签, 起点, 终点)。
// 标签即契约 business_id 里的 {period} 段：YYYY-MM / YYYY-Qn / YYYY。
func budgetPeriodBucket(period string, at time.Time) (label string, start, end time.Time) {
	loc := at.Location()
	year, month := int(at.Year()), int(at.Month())
	switch period {
	case "yearly":
		return fmt.Sprintf("%04d", year),
			time.Date(year, 1, 1, 0, 0, 0, 0, loc),
			time.Date(year+1, 1, 1, 0, 0, 0, 0, loc)
	case "quarterly":
		q := (month-1)/3 + 1
		qStartMonth := time.Month((q-1)*3 + 1)
		return fmt.Sprintf("%04d-Q%d", year, q),
			time.Date(year, qStartMonth, 1, 0, 0, 0, 0, loc),
			time.Date(year, qStartMonth+3, 1, 0, 0, 0, 0, loc)
	default: // monthly（含未知取值，退化为月桶 —— 与预算建表时的默认周期一致）
		return fmt.Sprintf("%04d-%02d", year, int(month)),
			time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc),
			time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, loc)
	}
}

// checkBudgetExceededAndPublish 在业务事务内检测「这条 expense 流水把哪个预算打爆了」，
// 并对每一个被超支的 (budget, period) 各写一条 finance.budget.exceeded outbox 行。
//
// 为什么挂在流水写入的同一事务里（而不是 handler 提交之后）：outbox 的前提是
// 「业务行与待发事件同 commit 或同 rollback」（§3.4.6、PRD 3.4「发布前落盘」）；
// 提交后再补写，进程在两步之间崩溃就永久丢事件，这正是 outbox 要消灭的形态。
//
// 与 service.BudgetAlertService.CheckAndAlert 的关系：那支 service 今天没有任何调用方
// （与 BusPublisher 一样属于「声明了未接线」），本函数是契约事件的真实发布路径；
// 两者的检测口径一致（本类别该周期 expense 合计 > 预算额 → 超支）。
func (r *FinanceRepo) checkBudgetExceededAndPublish(ctx context.Context, tx *gorm.DB, t *model.FinanceTransaction) error {
	if t == nil {
		return nil
	}
	if t.Type != "expense" || t.CategoryID == nil || *t.CategoryID == "" {
		return nil
	}

	var budgets []model.FinanceBudget
	if err := tx.Where("family_id = ? AND category_id = ? AND is_active = ? AND deleted_at IS NULL",
		t.FamilyID, *t.CategoryID, true).Find(&budgets).Error; err != nil {
		return fmt.Errorf("failed to list budgets for exceeded check: %w", err)
	}
	if len(budgets) == 0 {
		return nil
	}

	for _, budget := range budgets {
		label, start, end := budgetPeriodBucket(budget.Period, t.OccurredAt)
		if t.OccurredAt.Before(budget.StartDate) || !t.OccurredAt.Before(budget.EndDate) {
			continue // 流水落在预算有效期外
		}

		// expense 的 amount_cents 以负数入库（handler.CreateTransaction 的取负规则），
		// 所以「已花」是 -SUM；COALESCE 让空集读成 0 而不是 NULL。
		var spent int64
		if err := tx.Model(&model.FinanceTransaction{}).
			Where("family_id = ? AND category_id = ? AND type = ? AND occurred_at >= ? AND occurred_at < ? AND deleted_at IS NULL",
				t.FamilyID, *t.CategoryID, "expense", start, end).
			Select("COALESCE(SUM(-amount_cents), 0)").
			Scan(&spent).Error; err != nil {
			return fmt.Errorf("failed to sum spent for budget %s: %w", budget.ID, err)
		}

		if spent <= budget.AmountCents {
			continue
		}

		b := budget
		if err := writeContractEvent(ctx, tx, budget.FamilyID,
			budgetExceededEnvelope(&b, label, spent, t.OccurredAt)); err != nil {
			return err
		}
	}

	return nil
}
