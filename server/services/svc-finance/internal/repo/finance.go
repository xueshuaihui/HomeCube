// Package repo provides data access operations for the finance service.
package repo

import (
	"context"
	"crypto/rand"
	"encoding/json"
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

	// ErrNotFound 是「资源不存在」的哨兵。
	//
	// 为什么必须有它：下面所有 repo 方法此前都用裸 `errors.New("<entity> not found")`，
	// handler 拿到的也只是普通 error，只能 `err != nil` 一刀切，于是
	// 「id 不存在」和「数据库真的坏了」一起被答成 **500**。实测 11 条按 id 操作的路由
	// （accounts/:id/archive、categories/:id/deactivate、bills/:id/pay、loans/:id/payoff …）
	// 用一个不存在的 id 调用，全部返回 500。
	//
	// 语义上「不存在」是 404 而不是 500 —— 客户端要能区分「资源没了」和「服务坏了」，
	// 前者该重试/换 id，后者该报警。用哨兵 + errors.Is 判定，判定不依赖错误字符串，
	// 因此改文案不会把 404 悄悄变成 500。
	ErrNotFound = errors.New("finance resource not found")
)

// notFoundf 返回一个**能被 errors.Is(err, ErrNotFound) 识别**的「不存在」错误，
// 同时保留具体的实体名供日志与响应体使用。
func notFoundf(entity string, id string) error {
	return fmt.Errorf("%s %s: %w", entity, id, ErrNotFound)
}

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

// ── 家庭边界（PRD 15.2「判定以 family_id 为界」/ 18.2 验收第 8 条）────────────────
//
// 本文件里每一条**按 id 读写已存在资源**的查询都必须带 family_id，形如
// `WHERE id = ? AND family_id = ?`，0 行时返回 ErrNotFound（→ handler 答 404）。
// 照的是 svc-homeos 的口径（repo/homeos.go:521 `WHERE id = ? AND family_id = ?`）。
//
// 为什么不能只按 id 查、由 handler 事后比对：实测 GET /api/finance/transactions/:id
// 用 A 家庭的 token 拿到 200 + B 家庭流水的**整行**（family_id / created_by /
// category_id / account_id / version / receipt_file_id），因为
// `GetTransactionByID` 当时是 `WHERE id = ?`。任何「先读出来再看归属」的写法
// 都把跨家庭存在性变成了可读信号（404 vs 200 本身就是 oracle）。
//
// 参数顺序统一为 (ctx, familyID, id...)，与既有的 GetTagByID / GetRecurringRuleByID 一致。
// familyID 为空时**不放宽**而是查不到（id = ? AND family_id = '' 命中 0 行）：
// handler 侧的 session 缺失已在 401 挡掉，这里是第二道 fail closed 网。

// GetAccountByID retrieves an account by its ID within the given family.
func (r *FinanceRepo) GetAccountByID(ctx context.Context, familyID, id string) (*model.FinanceAccount, error) {
	var account model.FinanceAccount
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&account)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("account", id)
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

// ArchiveAccount archives an account (only if balance is zero) within the given family.
func (r *FinanceRepo) ArchiveAccount(ctx context.Context, familyID, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account model.FinanceAccount
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&account)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("account", id)
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

// GetCategoryByID retrieves a category by its ID within the given family.
func (r *FinanceRepo) GetCategoryByID(ctx context.Context, familyID, id string) (*model.FinanceCategory, error) {
	var category model.FinanceCategory
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&category)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("category", id)
		}
		return nil, fmt.Errorf("failed to get category: %w", result.Error)
	}
	return &category, nil
}

// AssertRefsExist 校验一笔流水引用的账户与分类都真实存在于**该家庭**。
//
// 存在的原因：CreateTransactionRequest 的 binding 只有 `required,uuid`，那只管格式。
// 迁移 0001 的 finance_transaction 也没有外键约束（account_id 上只有普通索引），
// 所以「格式合法但指向不存在的行」会被照单全收 —— G4 数据门禁实测就是靠这条查出了
// 4 条 account_id = 全零 UUID 的孤儿流水，账户余额与分类统计因此对不上。
//
// 为什么连 family 一起校验：只按 id 查是不够的 —— A 家庭的账户 id 被 B 家庭拿去用
// 同样是一条对不上账的流水。所以每次都带 family_id，跨家庭的引用在写入侧就挡住。
//
// categoryID 为 nil 表示「未分类」，这是合法状态（迁移 0001 里 category_id 可空），
// 因此只在非 nil 时校验。accountID 为空同理表示「本次没有账户引用」，也跳过 ——
// 记账接口上的「必须有账户」仍由 binding 的 `required,uuid` 保证，
// 而这个跳过让同一个校验能复用到只有分类引用的场合（POST /budgets）。
func (r *FinanceRepo) AssertRefsExist(ctx context.Context, familyID, accountID string, categoryID *string) error {
	var n int64
	if accountID != "" {
		if err := r.db.WithContext(ctx).
			Model(&model.FinanceAccount{}).
			Where("id = ? AND family_id = ? AND deleted_at IS NULL", accountID, familyID).
			Count(&n).Error; err != nil {
			return fmt.Errorf("failed to verify account: %w", err)
		}
		if n == 0 {
			return notFoundf("account", accountID)
		}
	}

	if categoryID != nil && *categoryID != "" {
		var m int64
		if err := r.db.WithContext(ctx).
			Model(&model.FinanceCategory{}).
			Where("id = ? AND family_id = ? AND deleted_at IS NULL", *categoryID, familyID).
			Count(&m).Error; err != nil {
			return fmt.Errorf("failed to verify category: %w", err)
		}
		if m == 0 {
			return notFoundf("category", *categoryID)
		}
	}
	return nil
}

// AssertTransactionsInFamily 校验若干笔流水都真实存在于**该家庭**。
//
// 与 AssertRefsExist 同一个理由，只是引用对象换成了 transaction_id：
// POST /split-settlements 会把 body 里的 transaction_id 直接写进
// finance_split_settlement.transaction_id（迁移 0001 上没有外键约束），
// PUT /invoices/:id/reimburse 同理。只校验「像个 UUID」= 谁都能把别人家庭的一笔
// 流水挂到自己家庭的 AA 单 / 报销单上，并且从「201 还是 404」里免费读出对方流水是否存在。
//
// 空 id 视为未引用（AA 单允许先不挂流水），不报错；familyID 为空则任何 id 都查不到，
// 方向同样是 fail closed（PRD 15.2）。
func (r *FinanceRepo) AssertTransactionsInFamily(ctx context.Context, familyID string, ids ...string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		var n int64
		if err := r.db.WithContext(ctx).
			Model(&model.FinanceTransaction{}).
			Where("id = ? AND family_id = ? AND deleted_at IS NULL", id, familyID).
			Count(&n).Error; err != nil {
			return fmt.Errorf("failed to verify transaction: %w", err)
		}
		if n == 0 {
			return notFoundf("transaction", id)
		}
	}
	return nil
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

// DeactivateCategory deactivates a category within the given family (does not affect historical transactions).
func (r *FinanceRepo) DeactivateCategory(ctx context.Context, familyID, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var category model.FinanceCategory
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&category)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("category", id)
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
	// Replay 承接「重放首次响应」的结果（PRD 14.7 / 迁移 0003 注释「重放返回首次响应
	// 而非重复执行」）。同一个 client_request_id 再来一次时，sync.CheckIdempotency
	// 会把首次的响应快照放进这里，handler 照它原样回给客户端。
	//
	// 旧实现直接 `return ErrDuplicateRequest`，于是重复提交得到 **409**。那不是幂等，
	// 是拒绝：客户端在网络超时后重试会看到「冲突」，无法判断自己的第一次写入到底
	// 成功了没有 —— 幂等键的全部意义就是让重试安全。
	Replay *model.FinanceTransaction
}

// CreateTransaction creates a new finance transaction with idempotency and optimistic locking.
func (r *FinanceRepo) CreateTransaction(ctx context.Context, input *CreateTransactionInput) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Check idempotency
		if input.ClientReqID != "" {
			exists, snapshot, err := r.sync.CheckIdempotency(ctx, tx, input.ClientReqID, input.FamilyID, input.RequestData)
			if err != nil {
				return fmt.Errorf("failed to check idempotency: %w", err)
			}
			if exists {
				// 重放：把首次创建的记录回填给调用方，由 handler 原样返回。
				// 快照里存的是首次响应体（RecordIdempotency 传的就是 input.Transaction），
				// 因此这里能还原出同一个 id —— 客户端看到的是「我的第一次提交成功了」，
				// 而不是 409。
				prior := &model.FinanceTransaction{}
				if len(snapshot) > 0 {
					if uerr := json.Unmarshal(snapshot, prior); uerr == nil && prior.ID != "" {
						input.Replay = prior
						return nil
					}
				}
				// 快照读不出来（历史数据/格式变更）时不能静默重复插入 —— 那会真的记两笔账。
				// 此时仍以冲突告知，并带上「首次确实已处理」这个事实。
				return fmt.Errorf("client_request_id %q 已处理但无法重放首次响应: %w",
					input.ClientReqID, ErrDuplicateRequest)
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

// GetTransactionByID retrieves a transaction by its ID within the given family.
func (r *FinanceRepo) GetTransactionByID(ctx context.Context, familyID, id string) (*model.FinanceTransaction, error) {
	var transaction model.FinanceTransaction
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&transaction)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("transaction", id)
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

// UpdateTransaction updates a transaction within the given family, with optimistic locking.
func (r *FinanceRepo) UpdateTransaction(ctx context.Context, familyID, id string, updates map[string]interface{}, expectedVersion int64) (*model.FinanceTransaction, error) {
	return nil, r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transaction model.FinanceTransaction
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&transaction)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("transaction", id)
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
		result = tx.Where("id = ? AND family_id = ?", id, familyID).First(&transaction)
		if result.Error != nil {
			return fmt.Errorf("failed to reload transaction: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, transaction.FamilyID, "transaction", transaction.ID, "UPDATE", transaction.Version, transaction)
	})
}

// SoftDeleteTransaction soft-deletes a transaction inside the given family (GORM soft delete via DeletedAt).
func (r *FinanceRepo) SoftDeleteTransaction(ctx context.Context, familyID, id string, deletedBy string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transaction model.FinanceTransaction
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&transaction)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("transaction", id)
			}
			return fmt.Errorf("failed to get transaction: %w", result.Error)
		}

		// Update metadata before soft delete
		updates := map[string]interface{}{
			"deleted_by": deletedBy,
			"version":    transaction.Version + 1,
		}
		if err := tx.Model(&transaction).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update transaction metadata: %w", err)
		}

		// GORM soft delete: sets deleted_at timestamp (model has gorm.DeletedAt field)
		result = tx.Delete(&transaction)
		if result.Error != nil {
			return fmt.Errorf("failed to soft-delete transaction: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return errors.New("no rows affected during soft delete")
		}

		return r.sync.AppendChangeLog(ctx, tx, transaction.FamilyID, "transaction", transaction.ID, "DELETE", transaction.Version+1, transaction)
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

// GetLedgerByID retrieves a ledger by its ID within the given family.
func (r *FinanceRepo) GetLedgerByID(ctx context.Context, familyID, id string) (*model.FinanceLedger, error) {
	var ledger model.FinanceLedger
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&ledger)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("ledger", id)
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

// GetBudgetByID retrieves a budget by its ID within the given family.
func (r *FinanceRepo) GetBudgetByID(ctx context.Context, familyID, id string) (*model.FinanceBudget, error) {
	var budget model.FinanceBudget
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&budget)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("budget", id)
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

// GetBillByID retrieves a bill by its ID within the given family.
func (r *FinanceRepo) GetBillByID(ctx context.Context, familyID, id string) (*model.FinanceBill, error) {
	var bill model.FinanceBill
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&bill)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("bill", id)
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
func (r *FinanceRepo) MarkAsPaid(ctx context.Context, familyID, id string) (*model.FinanceBill, error) {
	var updatedBill *model.FinanceBill
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bill model.FinanceBill
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&bill)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("bill", id)
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

// GetLoanByID retrieves a loan by its ID within the given family.
func (r *FinanceRepo) GetLoanByID(ctx context.Context, familyID, id string) (*model.FinanceLoan, error) {
	var loan model.FinanceLoan
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&loan)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("loan", id)
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
func (r *FinanceRepo) PayOffLoan(ctx context.Context, familyID, id string) (*model.FinanceLoan, error) {
	var updatedLoan *model.FinanceLoan
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var loan model.FinanceLoan
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&loan)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("loan", id)
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

// GetRepaymentPlanByID retrieves a repayment plan by its ID within the given family.
func (r *FinanceRepo) GetRepaymentPlanByID(ctx context.Context, familyID, id string) (*model.FinanceRepaymentPlan, error) {
	var plan model.FinanceRepaymentPlan
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&plan)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("repayment_plan", id)
		}
		return nil, fmt.Errorf("failed to get repayment plan: %w", result.Error)
	}
	return &plan, nil
}

// GetRepaymentPlansByLoanID retrieves all repayment plans for a loan of the given family.
//
// 两个条件都要：plans 行自己带 family_id，只按 loan_id 过滤等于把「这条 loan 属于谁」
// 交给客户端判断 —— 猜到别人的 loan_id 就能读出对方家庭整条还款计划。
func (r *FinanceRepo) GetRepaymentPlansByLoanID(ctx context.Context, familyID, loanID string) ([]model.FinanceRepaymentPlan, error) {
	var plans []model.FinanceRepaymentPlan
	result := r.db.WithContext(ctx).
		Where("loan_id = ? AND family_id = ? AND deleted_at IS NULL", loanID, familyID).
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
func (r *FinanceRepo) MarkRepaymentPlanAsPaid(ctx context.Context, familyID, id string) (*model.FinanceRepaymentPlan, error) {
	var updatedPlan *model.FinanceRepaymentPlan
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.FinanceRepaymentPlan
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&plan)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("repayment_plan", id)
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

// GetGoalByID retrieves a goal by its ID within the given family.
func (r *FinanceRepo) GetGoalByID(ctx context.Context, familyID, id string) (*model.FinanceGoal, error) {
	var goal model.FinanceGoal
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&goal)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("goal", id)
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
func (r *FinanceRepo) UpdateGoalProgress(ctx context.Context, familyID, id string, currentAmountCents int64) (*model.FinanceGoal, error) {
	var updatedGoal *model.FinanceGoal
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var goal model.FinanceGoal
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&goal)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("goal", id)
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

// GetSplitSettlementByID retrieves a split settlement by its ID within the given family.
func (r *FinanceRepo) GetSplitSettlementByID(ctx context.Context, familyID, id string) (*model.FinanceSplitSettlement, error) {
	var settlement model.FinanceSplitSettlement
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&settlement)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("split_settlement", id)
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

// AddParticipant adds a participant to a split settlement of the given family.
//
// familyID 是**会话家庭**，不是客户端声明值：finance_participant 行本身没有 family_id 列
// （model/finance.go:238-241 只有 settlement_id / account_id），所以它的归属只能由
// 「父 settlement 是否属于本家庭」推导。少了这一层，任何人猜到别人的 settlement_id
// 就能往对方那笔 AA 里塞一条分摊记录。
func (r *FinanceRepo) AddParticipant(ctx context.Context, familyID string, participant *model.FinanceParticipant) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Verify settlement exists, belongs to this family, and is in draft status
		var settlement model.FinanceSplitSettlement
		result := tx.Where("id = ? AND family_id = ?", participant.SettlementID, familyID).First(&settlement)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("split_settlement", participant.SettlementID)
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
func (r *FinanceRepo) SettleSplit(ctx context.Context, familyID, id string) (*model.FinanceSplitSettlement, error) {
	var updatedSettlement *model.FinanceSplitSettlement
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var settlement model.FinanceSplitSettlement
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&settlement)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("split_settlement", id)
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

// GetParticipantsBySettlement retrieves all participants for a split settlement of the given family.
//
// 与 AddParticipant 同一个理由：finance_participant 没有 family_id 列，归属只能由父
// settlement 决定，所以这里先确认 settlement 属于本家庭，再列它的分摊明细。
// 直接按 settlement_id 查等于把「别人的 settlement_id」变成一个可读接口
// （实测 GET /split-settlements/:id/participants 只带 settlement_id 条件）。
func (r *FinanceRepo) GetParticipantsBySettlement(ctx context.Context, familyID, settlementID string) ([]model.FinanceParticipant, error) {
	var owned int64
	if err := r.db.WithContext(ctx).Model(&model.FinanceSplitSettlement{}).
		Where("id = ? AND family_id = ? AND deleted_at IS NULL", settlementID, familyID).
		Count(&owned).Error; err != nil {
		return nil, fmt.Errorf("failed to verify split settlement: %w", err)
	}
	if owned == 0 {
		return nil, notFoundf("split_settlement", settlementID)
	}

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

// GetCreditCardByID retrieves a credit card by its ID within the given family.
func (r *FinanceRepo) GetCreditCardByID(ctx context.Context, familyID, id string) (*model.FinanceCreditCard, error) {
	var card model.FinanceCreditCard
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&card)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("credit_card", id)
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
func (r *FinanceRepo) UpdateCreditCardBalance(ctx context.Context, familyID, id string, newBalanceCents int64) (*model.FinanceCreditCard, error) {
	var updatedCard *model.FinanceCreditCard
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var card model.FinanceCreditCard
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&card)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("credit_card", id)
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

// GetInvoiceByID retrieves an invoice by its ID within the given family.
func (r *FinanceRepo) GetInvoiceByID(ctx context.Context, familyID, id string) (*model.FinanceInvoice, error) {
	var invoice model.FinanceInvoice
	result := r.db.WithContext(ctx).Where("id = ? AND family_id = ?", id, familyID).First(&invoice)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("invoice", id)
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
//
// 两处家庭边界（PRD 15.2）：
//  1. 发票行按 id + family_id 取，别人家庭的发票一律 not found；
//  2. 关联的 transaction_id 必须**也**属于本家庭。此前它是原样落库的
//     （invoice.transaction_id = 客户端给的任意 UUID），于是 A 家庭能把 B 家庭的一笔
//     流水挂到自己发票的报销凭证上 —— 既是数据污染，也是一条跨家庭的引用存在性探测。
func (r *FinanceRepo) MarkInvoiceAsReimbursed(ctx context.Context, familyID, id string, status string, reason *string, transactionID *string) (*model.FinanceInvoice, error) {
	var updatedInvoice *model.FinanceInvoice
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var invoice model.FinanceInvoice
		result := tx.Where("id = ? AND family_id = ?", id, familyID).First(&invoice)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("invoice", id)
			}
			return fmt.Errorf("failed to get invoice: %w", result.Error)
		}

		// 关联流水归属校验（同一事务，校验与写入同一个提交点）。
		if transactionID != nil && *transactionID != "" {
			var n int64
			if err := tx.Model(&model.FinanceTransaction{}).
				Where("id = ? AND family_id = ? AND deleted_at IS NULL", *transactionID, familyID).
				Count(&n).Error; err != nil {
				return fmt.Errorf("failed to verify transaction: %w", err)
			}
			if n == 0 {
				return notFoundf("transaction", *transactionID)
			}
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
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("asset_liability_report", familyID)
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
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("asset_liability_report", familyID)
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
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("tag", tagID)
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
				return notFoundf("tag", tagID)
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
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("recurring_rule", ruleID)
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
//
// familyID 由调用方（周期记账 worker）从规则行本身带进来 —— 它不是客户端给的，但
// 「按 id 更新」同样要带家庭条件，否则任何拿到别人 rule_id 的代码路径都能改写它。
func (r *FinanceRepo) MarkRecurringRuleExecuted(ctx context.Context, familyID, ruleID string, nextExecuteAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&model.FinanceRecurringRule{}).
			Where("id = ? AND family_id = ?", ruleID, familyID).
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

// DeleteRecurringRule soft-deletes a recurring rule.
func (r *FinanceRepo) DeleteRecurringRule(ctx context.Context, familyID string, ruleID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rule model.FinanceRecurringRule
		result := tx.Where("id = ? AND family_id = ?", ruleID, familyID).First(&rule)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("recurring_rule", ruleID)
			}
			return fmt.Errorf("failed to get recurring rule: %w", result.Error)
		}

		result = tx.Where("id = ? AND family_id = ?", ruleID, familyID).Delete(&rule)
		if result.Error != nil {
			return fmt.Errorf("failed to delete recurring rule: %w", result.Error)
		}

		return r.sync.AppendChangeLog(ctx, tx, familyID, "recurring_rule", ruleID, "DELETE", 1, rule)
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
			// 「不存在」用哨兵错误表达，不再 return nil, nil：返回 (nil, nil) 让调用方
			// 的 `if err != nil` 抓不到，随后解引用 nil 直接 panic —— 实测
			// DELETE /transactions/{id} 打一个不存在的 id 就是 runtime error: invalid
			// memory address or nil pointer dereference（gin Recovery 吞成空体 500）。
			return nil, notFoundf("budget_period", periodID)
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
				return notFoundf("budget_period", periodID)
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

// ==================== Trash Operations ====================

// DeletedTransaction represents a transaction in the trash (with deleted_at set).
type DeletedTransaction struct {
	model.FinanceTransaction
	DeletedAtTime time.Time `json:"deleted_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	CanRestore    bool      `json:"can_restore"`
}

// ListDeletedTransactions retrieves all soft-deleted transactions for a family within the retention period (30 days).
func (r *FinanceRepo) ListDeletedTransactions(ctx context.Context, familyID string) ([]DeletedTransaction, error) {
	// Calculate the cutoff date (30 days ago)
	cutoffDate := time.Now().Add(-30 * 24 * time.Hour)

	var transactions []model.FinanceTransaction
	result := r.db.WithContext(ctx).Unscoped().
		Where("family_id = ? AND deleted_at IS NOT NULL AND deleted_at > ?", familyID, cutoffDate).
		Order("deleted_at DESC").
		Find(&transactions)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list deleted transactions: %w", result.Error)
	}

	// Convert to DeletedTransaction with metadata
	deletedTransactions := make([]DeletedTransaction, 0, len(transactions))
	for _, tx := range transactions {
		expiresAt := tx.DeletedAt.Time.Add(30 * 24 * time.Hour)
		canRestore := time.Now().Before(expiresAt)

		deletedTx := DeletedTransaction{
			FinanceTransaction: tx,
			DeletedAtTime:      tx.DeletedAt.Time,
			ExpiresAt:          expiresAt,
			CanRestore:         canRestore,
		}
		deletedTransactions = append(deletedTransactions, deletedTx)
	}

	return deletedTransactions, nil
}

// RestoreTransaction restores a soft-deleted transaction by clearing its deleted_at field.
// It also recalculates the account balance since deletion had deducted the amount.
func (r *FinanceRepo) RestoreTransaction(ctx context.Context, id string, familyID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transaction model.FinanceTransaction
		// Use Unscoped() to find soft-deleted records
		result := tx.Unscoped().Where("id = ? AND family_id = ?", id, familyID).First(&transaction)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("transaction", id)
			}
			return fmt.Errorf("failed to get deleted transaction: %w", result.Error)
		}

		// Verify it's actually deleted
		if transaction.DeletedAt.Time.IsZero() {
			return errors.New("transaction is not deleted")
		}

		// Clear the deleted_at and deleted_by fields
		now := time.Now()
		transaction.DeletedAt = gorm.DeletedAt{}
		transaction.DeletedBy = nil
		transaction.UpdatedAt = now
		transaction.Version++

		// Save the restored transaction
		result = tx.Save(&transaction)
		if result.Error != nil {
			return fmt.Errorf("failed to restore transaction: %w", result.Error)
		}

		// Recalculate account balance: add back the transaction amount that was deducted on delete
		// Note: The balance recalculation should be handled by the BalanceService after restoration
		// Here we just mark the change log
		return r.sync.AppendChangeLog(ctx, tx, familyID, "transaction", transaction.ID, "RESTORE", transaction.Version, transaction)
	})
}

// PermanentlyDeleteTransaction permanently deletes a transaction from the database.
func (r *FinanceRepo) PermanentlyDeleteTransaction(ctx context.Context, id string, familyID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transaction model.FinanceTransaction
		// Use Unscoped() to find soft-deleted records
		result := tx.Unscoped().Where("id = ? AND family_id = ?", id, familyID).First(&transaction)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return notFoundf("transaction", id)
			}
			return fmt.Errorf("failed to get deleted transaction: %w", result.Error)
		}

		// Verify it's actually deleted
		if transaction.DeletedAt.Time.IsZero() {
			return errors.New("cannot permanently delete an active transaction")
		}

		// Actually delete the record (hard delete)
		result = tx.Unscoped().Delete(&transaction)
		if result.Error != nil {
			return fmt.Errorf("failed to permanently delete transaction: %w", result.Error)
		}

		// Append change log for permanent deletion
		return r.sync.AppendChangeLog(ctx, tx, familyID, "transaction", transaction.ID, "PERMANENT_DELETE", transaction.Version, transaction)
	})
}

// ClearExpiredTrash removes all trash items older than 30 days.
func (r *FinanceRepo) ClearExpiredTrash(ctx context.Context, familyID string) (int64, error) {
	// Calculate the cutoff date (30 days ago)
	cutoffDate := time.Now().Add(-30 * 24 * time.Hour)

	// Count expired items first
	var count int64
	result := r.db.WithContext(ctx).Unscoped().
		Model(&model.FinanceTransaction{}).
		Where("family_id = ? AND deleted_at IS NOT NULL AND deleted_at < ?", familyID, cutoffDate).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count expired trash items: %w", result.Error)
	}

	if count == 0 {
		return 0, nil
	}

	// Delete expired items
	result = r.db.WithContext(ctx).Unscoped().
		Where("family_id = ? AND deleted_at IS NOT NULL AND deleted_at < ?", familyID, cutoffDate).
		Delete(&model.FinanceTransaction{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to clear expired trash: %w", result.Error)
	}

	return count, nil
}

// ==================== Finance Settings Operations ====================

// GetSettingsByFamily retrieves finance settings for a family. Returns nil if not found.
func (r *FinanceRepo) GetSettingsByFamily(ctx context.Context, familyID string) (*model.FinanceSettings, error) {
	var settings model.FinanceSettings
	result := r.db.WithContext(ctx).
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		First(&settings)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// 「还没有设置」在本方法里是**正常状态**而不是错误：handler.GetFinanceSettings
			// 的语义是「查不到就建一份默认」（finance.go:1911 的 `if settings == nil`），
			// 因此这里必须返回 (nil, nil) 而不是 ErrNotFound —— 否则 handler 会答 404，
			// 家庭第一次打开设置页就看到「不存在」而不是默认值。
			//
			// 这与本文件其他 Get*ByID 刻意不同：那些方法的调用方都会直接解引用返回值，
			// 用 nil 表达不存在会让 `if err != nil` 抓不到、随后 panic（实测 DELETE
			// /transactions/{id} 打不存在的 id 即 runtime error，被 gin Recovery 吞成空体 500）。
			// 判据是「调用方是否解引用」，不是「是否查不到」。
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get finance settings: %w", result.Error)
	}
	return &settings, nil
}

// UpsertSettings creates or updates finance settings for a family.
// Uses INSERT ... ON CONFLICT for atomic upsert operation.
func (r *FinanceRepo) UpsertSettings(ctx context.Context, settings *model.FinanceSettings) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Generate ID if not set
		if settings.ID == "" {
			settings.ID = generateUUID()
		}

		// Check if settings already exist
		var existing model.FinanceSettings
		result := tx.Where("family_id = ? AND deleted_at IS NULL", settings.FamilyID).First(&existing)

		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("failed to query existing settings: %w", result.Error)
		}

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Create new settings
			if err := tx.Create(settings).Error; err != nil {
				return fmt.Errorf("failed to create finance settings: %w", err)
			}
		} else {
			// Update existing settings
			settings.ID = existing.ID
			settings.Version = existing.Version + 1
			settings.CreatedAt = existing.CreatedAt

			if err := tx.Save(settings).Error; err != nil {
				return fmt.Errorf("failed to update finance settings: %w", err)
			}
		}

		// Append change log
		return r.sync.AppendChangeLog(ctx, tx, settings.FamilyID, "settings", settings.ID, "UPDATE", settings.Version, settings)
	})
}

// CreateDefaultSettings creates default finance settings for a new family.
func (r *FinanceRepo) CreateDefaultSettings(ctx context.Context, familyID string) (*model.FinanceSettings, error) {
	settings := &model.FinanceSettings{
		FamilyID:              familyID,
		CurrencyUnit:          "CNY",
		DecimalPlaces:         2,
		BudgetAlertThreshold:  0.80,
		AutoCategorizeEnabled: false,
		ReceiptOCREnabled:     false,
		VoiceInputEnabled:     false,
		Version:               1,
	}

	if err := r.UpsertSettings(ctx, settings); err != nil {
		return nil, err
	}

	return settings, nil
}
