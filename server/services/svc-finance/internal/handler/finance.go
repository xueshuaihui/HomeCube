// Package handler provides HTTP handlers for the finance service.
package handler

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/service"
)

// FinanceHandler holds dependencies for finance handlers.
type FinanceHandler struct {
	repo               *repo.FinanceRepo
	balanceService     *service.BalanceService
	statisticsService  *service.StatisticsService
	budgetAlertService *service.BudgetAlertService
	exportService      *service.ExportService
	billService        *service.BillService

	// OnDenied 是 PRD 15.5「越权尝试全部落审计」的写入口，由 cmd/svc-finance/main.go 接到
	// 鉴权中间件的 OnDenied（同一个 sink，同一份事件名）。
	//
	// 为什么在 handler 上而不是只在中间件里：中间件只能看见**客户端声明的 family_id**，
	// 看不见「路径里的资源 id 属不属于本家庭」——那要查过库才知道。家庭作用域改成以
	// sess.FamilyID 为唯一来源后，这类判定落在 handler 里，审计也必须能在同一处发起。
	// nil 时所有拒绝仍然照答（403/401/404），只是没有审计记录，见 scope.go 的 deny。
	OnDenied DenyFunc
}

// NewFinanceHandler creates a new finance handler instance.
func NewFinanceHandler(repo *repo.FinanceRepo, balanceService *service.BalanceService, statisticsService *service.StatisticsService, budgetAlertService *service.BudgetAlertService, exportService *service.ExportService, billService *service.BillService) *FinanceHandler {
	return &FinanceHandler{
		repo:               repo,
		balanceService:     balanceService,
		statisticsService:  statisticsService,
		budgetAlertService: budgetAlertService,
		exportService:      exportService,
		billService:        billService,
	}
}

// ==================== 错误 → HTTP 状态的统一映射 ====================

// respondRepoError 把 repo / service 层错误映射成正确的 HTTP 状态，并写出响应。
//
// 为什么必须有这个函数：此前所有写路径都是一刀切
// `c.JSON(500, gin.H{"error": "failed to X: " + err.Error()})`，于是「id 不存在」
// 和「数据库真的坏了」返回同一个 500。实测 11 条按 id 操作的路由用一个不存在的 id
// 调用，全部 500：
//
//	PUT    /api/finance/accounts/:id/archive          500 "account not found"
//	PUT    /api/finance/categories/:id/deactivate     500 "category not found"
//	PUT    /api/finance/bills/:id/pay                 500 "bill not found"
//	PUT    /api/finance/loans/:id/payoff              500 "loan not found"
//	PUT    /api/finance/repayment-plans/:id/pay       500 "repayment plan not found"
//	PUT    /api/finance/split-settlements/:id/settle  500 "split settlement not found"
//	DELETE /api/finance/recurring/:id                 500 "recurring rule not found"
//	POST   /api/finance/trash/:id/restore             500 "not found or already restored"
//	DELETE /api/finance/trash/:id                     500 "transaction not found"
//	PUT    /api/finance/transactions/:id              500（空体）
//	DELETE /api/finance/transactions/:id              500
//
// 语义上「资源不存在」是 **404**、「乐观锁冲突」是 **409**、其余才是 500。
// 客户端要能区分这三种情况：404 该换 id 或提示用户，409 该重读后重试，
// 500 才该报警。把它们混成 500 的直接后果是监控误报 + 前端无法给出正确提示。
//
// 判定全部用 errors.Is 走哨兵错误，不匹配错误字符串 —— 改文案不会把 404 悄悄变回 500。
func respondRepoError(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": op + ": " + err.Error()})
	case errors.Is(err, repo.ErrOptimisticLock):
		c.JSON(http.StatusConflict, gin.H{"error": op + ": " + err.Error()})
	case errors.Is(err, repo.ErrAccountBalanceNonZero):
		c.JSON(http.StatusConflict, gin.H{"error": op + ": " + err.Error()})
	case errors.Is(err, repo.ErrDuplicateRequest):
		c.JSON(http.StatusConflict, gin.H{"error": op + ": " + err.Error()})
	case errors.Is(err, repo.ErrInvalidSplitStatusTransition),
		errors.Is(err, repo.ErrSplitAmountMismatch),
		errors.Is(err, repo.ErrInvalidInvoiceStatusTransition),
		errors.Is(err, repo.ErrAssetLiabilityMismatch):
		// 状态机/金额校验类：请求与当前状态不符，属业务冲突而非服务器故障。
		c.JSON(http.StatusConflict, gin.H{"error": op + ": " + err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": op + ": " + err.Error()})
	}
}

// ==================== Account Handlers ====================

// CreateAccount handles POST /api/finance/accounts.
//
// body 里的 family_id 只为契约兼容而保留：它被**校验**（与 token 家庭不一致 → 403 + 审计），
// 从不被**使用** —— 落库的家庭永远是 session 的家庭。
func (h *FinanceHandler) CreateAccount(c *gin.Context) {
	var req struct {
		FamilyID string `json:"family_id" binding:"required,uuid"`
		Name     string `json:"name" binding:"required"`
		Type     string `json:"type" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	account := &model.FinanceAccount{
		FamilyID: familyID,
		Name:     req.Name,
		Type:     req.Type,
		Balance:  0,
		Version:  1,
	}

	if err := h.repo.CreateAccount(c.Request.Context(), account); err != nil {
		respondRepoError(c, "failed to create account", err)
		return
	}

	c.JSON(http.StatusCreated, account)
}

// ListAccounts handles GET /api/finance/accounts.
func (h *FinanceHandler) ListAccounts(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	accounts, err := h.repo.ListAccountsByFamily(c.Request.Context(), familyID)
	if err != nil {
		respondRepoError(c, "failed to list accounts", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": accounts})
}

// ArchiveAccount handles PUT /api/finance/accounts/{id}/archive.
func (h *FinanceHandler) ArchiveAccount(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	// familyID 进 repo：归档是写操作，只按 id 找等于谁都能归档别人家庭的账户。
	if err := h.repo.ArchiveAccount(c.Request.Context(), familyID, id); err != nil {
		if err == repo.ErrAccountBalanceNonZero {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		rejectScopedError(c, h.OnDenied, "failed to archive account", "account", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "account archived successfully"})
}

// ==================== Category Handlers ====================

// CreateCategory handles POST /api/finance/categories.
func (h *FinanceHandler) CreateCategory(c *gin.Context) {
	var req struct {
		FamilyID  string `json:"family_id" binding:"required,uuid"`
		Name      string `json:"name" binding:"required"`
		Icon      string `json:"icon"`
		SortOrder int32  `json:"sort_order"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	category := &model.FinanceCategory{
		FamilyID:  familyID,
		Name:      req.Name,
		Icon:      req.Icon,
		SortOrder: req.SortOrder,
		IsActive:  true,
		Version:   1,
	}

	if err := h.repo.CreateCategory(c.Request.Context(), category); err != nil {
		respondRepoError(c, "failed to create category", err)
		return
	}

	c.JSON(http.StatusCreated, category)
}

// ListCategories handles GET /api/finance/categories?is_active=.
func (h *FinanceHandler) ListCategories(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	var isActive *bool
	isActiveParam := c.Query("is_active")
	if isActiveParam != "" {
		val := isActiveParam == "true"
		isActive = &val
	}

	categories, err := h.repo.ListCategoriesByFamily(c.Request.Context(), familyID, isActive)
	if err != nil {
		respondRepoError(c, "failed to list categories", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": categories})
}

// DeactivateCategory handles PUT /api/finance/categories/{id}/deactivate.
func (h *FinanceHandler) DeactivateCategory(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "category id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	if err := h.repo.DeactivateCategory(c.Request.Context(), familyID, id); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to deactivate category", "category", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "category deactivated successfully"})
}

// ==================== Transaction Handlers ====================

// CreateTransactionRequest represents the request body for creating a transaction.
type CreateTransactionRequest struct {
	FamilyID        string    `json:"family_id" binding:"required,uuid"`
	Type            string    `json:"type" binding:"required,oneof=income expense transfer"`
	AmountCents     int64     `json:"amount_cents" binding:"required"`
	CategoryID      *string   `json:"category_id"`
	AccountID       string    `json:"account_id" binding:"required,uuid"`
	OccurredAt      time.Time `json:"occurred_at" binding:"required"`
	Description     string    `json:"description"`
	ReceiptFileID   *string   `json:"receipt_file_id"`
	TransferGroupID *string   `json:"transfer_group_id"`
	ClientRequestID *string   `json:"client_request_id"`
}

// CreateTransaction handles POST /api/finance/transactions.
//
// 家庭来源是 session，不是 body：`Content-Type: text/plain` 骗不过 gin 的 ShouldBindJSON，
// 所以一个声明了外家庭 family_id 的 body 以前会被原样绑定并写进对方账本（实测 201）。
// 现在 req.FamilyID 只用于「与 token 是否一致」的判定（不一致 → 403 + 审计），
// 落库的 FamilyID 恒为 sess.FamilyID。
func (h *FinanceHandler) CreateTransaction(c *gin.Context) {
	var req CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	// Validate amount based on type
	if req.Type == "expense" && req.AmountCents > 0 {
		req.AmountCents = -req.AmountCents // Make expense amounts negative
	} else if req.Type == "income" && req.AmountCents < 0 {
		req.AmountCents = -req.AmountCents // Make income amounts positive
	}

	// 引用存在性校验：account_id / category_id 必须在**本家庭**里真实存在。
	//
	// 为什么 binding 的 `required,uuid` 不够：那只保证「长得像一个 UUID」，
	// 不保证「指向一个存在的账户」。迁移 0001 里 finance_transaction.account_id 也没有
	// 外键约束（只有索引），于是写一个格式合法但随机造的 account_id 就能落库，
	// 留下一条永远对不上账的流水 —— 账户余额、分类统计、报表全部对不上，且无法从
	// 接口层发现（G4 数据门禁实测能查出这类孤儿行）。
	//
	// 校验放在写入之前：脏数据一旦落库就是脏数据，事后清理不如不让它进来。
	//
	// 传的是 familyID（= sess.FamilyID）而不是 req.FamilyID：这同时关掉了
	// 「B 家庭用自己的 token + A 家庭的 account_id 记一笔到 A 的账户上」这条引用侧漏 ——
	// 引用必须与**会话**同家庭，而不是与客户端自称的家庭同家庭。
	if err := h.repo.AssertRefsExist(c.Request.Context(), familyID, req.AccountID, req.CategoryID); err != nil {
		rejectScopedError(c, h.OnDenied, "invalid transaction references", "account/category", err)
		return
	}

	transaction := &model.FinanceTransaction{
		ID:              generateUUID(),
		FamilyID:        familyID,
		Type:            req.Type,
		AmountCents:     req.AmountCents,
		CategoryID:      req.CategoryID,
		AccountID:       req.AccountID,
		OccurredAt:      req.OccurredAt,
		Description:     req.Description,
		ReceiptFileID:   req.ReceiptFileID,
		TransferGroupID: req.TransferGroupID,
		ClientRequestID: req.ClientRequestID,
		Version:         1,
	}

	// created_by / visibility 的语义由另一张卡决定（本次只修家庭边界），这里刻意不填、不改。

	input := &repo.CreateTransactionInput{
		Transaction: transaction,
		ClientReqID: getStringValue(req.ClientRequestID),
		FamilyID:    familyID,
		RequestData: req,
	}

	if err := h.repo.CreateTransaction(c.Request.Context(), input); err != nil {
		// errors.Is, not ==: the repo returns the sentinel bare on an idempotency hit
		// (repo/finance.go:233) but wrapped with the driver's own words when the reused
		// client_request_id is caught by the database unique index instead
		// (repo/finance.go:243 -> isDuplicateClientRequestKey). With == the wrapped half fell
		// through to the 500 branch below, telling a retrying client "the server broke" about a
		// submission the server had already processed.
		if errors.Is(err, repo.ErrDuplicateRequest) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		respondRepoError(c, "failed to create transaction", err)
		return
	}

	// 幂等重放：同一个 client_request_id 第二次提交时，repo 已把首次创建的记录
	// 回填进 input.Replay（PRD 14.7「重放返回首次响应」）。这里原样回它，并标 200。
	//
	// 为什么不是 201：201 Created 意味着「服务端新建了一个资源」，而重放并没有新建。
	// 客户端靠这个状态码区分「我刚创建的」与「我之前创建过的、这次只是重试」。
	if input.Replay != nil {
		c.JSON(http.StatusOK, input.Replay)
		return
	}

	// 余额缓存同步：finance_account.balance 是建表时就有的列，不同步它就一直停在 0
	// （详见 BalanceService.RecalcAccountBalance 的注释）。失败只记不挡 ——
	// 流水已经落库了，此时报 500 会让客户端以为「记账失败」而重试，造成重复记账。
	if _, calcErr := h.balanceService.RecalcAccountBalance(c.Request.Context(), transaction.AccountID, familyID); calcErr != nil {
		c.Error(calcErr) //nolint:errcheck // 交给 obs 的错误中间件记日志
	}

	c.JSON(http.StatusCreated, transaction)
}

// ListTransactions handles GET /api/finance/transactions?period=&cursor=.
func (h *FinanceHandler) ListTransactions(c *gin.Context) {
	sess, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	period := c.Query("period")
	cursor := c.Query("cursor")
	limit := 50

	transactions, nextCursor, err := h.repo.ListTransactionsByFamily(c.Request.Context(), familyID, period, &cursor, limit)
	if err != nil {
		respondRepoError(c, "failed to list transactions", err)
		return
	}

	// L3 可见性过滤（PRD 15.4）：private 行只对作者与 owner 可见。
	//
	// 以前这里是 `if err == nil && sess != nil` —— 拿不到 session 就**跳过过滤**，
	// 也就是 fail open：任何让中间件没装 session 的路径都会把该隐藏的私有流水全暴露。
	// 现在 session 已在 scopeFamily 里 fail closed（无 session 直接 401，连查询都不发），
	// 所以过滤是无条件的。
	transactions = filterVisible(transactions, sess)

	response := gin.H{
		"items": transactions,
	}
	if nextCursor != nil {
		response["next_cursor"] = *nextCursor
	}

	c.JSON(http.StatusOK, response)
}

// GetTransaction handles GET /api/finance/transactions/{id}.
//
// 这条路由是本次缺陷的实证入口：一个 owner token 请求别人家庭的流水 id，
// 旧实现答 200 + 整行（family_id / created_by / category_id / account_id / version /
// receipt_file_id），并且完全绕过 L3 私有过滤。现在两件事一起修：
// repo 侧 `WHERE id = ? AND family_id = ?`（不属于本家庭 → 404，与「不存在」同形），
// 以及单条读也要过 canViewTransaction。
func (h *FinanceHandler) GetTransaction(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transaction id is required"})
		return
	}

	sess, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	transaction, err := h.repo.GetTransactionByID(c.Request.Context(), familyID, id)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "transaction not found or deleted", "transaction", err)
		return
	}

	if !canViewTransaction(transaction, sess) {
		// 同一条 private 规则在列表里是「不出现」，这里也答 404 —— 不能答 403，
		// 那等于确认「这条 id 存在，只是你不该看」。
		rejectScopedError(c, h.OnDenied, "transaction not found or deleted", "transaction", repo.ErrNotFound)
		return
	}

	c.JSON(http.StatusOK, transaction)
}

// UpdateTransactionRequest represents the request body for updating a transaction.
type UpdateTransactionRequest struct {
	Type            string     `json:"type" binding:"omitempty,oneof=income expense transfer"`
	AmountCents     *int64     `json:"amount_cents"`
	CategoryID      *string    `json:"category_id"`
	AccountID       *string    `json:"account_id"`
	OccurredAt      *time.Time `json:"occurred_at"`
	Description     *string    `json:"description"`
	ReceiptFileID   *string    `json:"receipt_file_id"`
	TransferGroupID *string    `json:"transfer_group_id"`
	Version         int64      `json:"version" binding:"required"`
}

// UpdateTransaction handles PUT /api/finance/transactions/{id}.
func (h *FinanceHandler) UpdateTransaction(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transaction id is required"})
		return
	}

	var req UpdateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	// 家庭作用域只来自 session；见 scope.go 的文件头。
	sess, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	// 更新前的账户要留着：换了账户时，原账户的余额也得重算（见本函数末尾）。
	//
	// 这一步「先读后改」的读也必须带家庭条件：不带的话跨家庭 id 会先把对方整行读出来
	// （回显进响应、并进余额重算），后面的 UPDATE 才拒 —— 读了就是泄露，拒了也来不及。
	before, err := h.repo.GetTransactionByID(c.Request.Context(), familyID, id)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "transaction not found or deleted", "transaction", err)
		return
	}

	// private 行只有作者与 owner 能改（PRD 15.4）：GET 挡住了、PUT 挡不住等于没挡。
	if !canViewTransaction(before, sess) {
		rejectScopedError(c, h.OnDenied, "transaction not found or deleted", "transaction", repo.ErrNotFound)
		return
	}

	updates := make(map[string]interface{})
	if req.Type != "" {
		updates["type"] = req.Type
	}
	if req.AmountCents != nil {
		updates["amount_cents"] = *req.AmountCents
	}
	if req.CategoryID != nil {
		updates["category_id"] = *req.CategoryID
	}
	if req.AccountID != nil {
		updates["account_id"] = *req.AccountID
	}
	if req.OccurredAt != nil {
		updates["occurred_at"] = *req.OccurredAt
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.ReceiptFileID != nil {
		updates["receipt_file_id"] = *req.ReceiptFileID
	}
	if req.TransferGroupID != nil {
		updates["transfer_group_id"] = *req.TransferGroupID
	}

	// 改引用同样要过「引用必须属于本家庭」（与 CreateTransaction 的 AssertRefsExist 同一口径）：
	// 否则 PUT 能把一条本家庭流水的 account_id 指向别人家庭的账户，
	// 余额算进对方账户、统计两头对不上，且这是一条免费的跨家庭 id 有效性探测。
	newAccountID := before.AccountID
	if req.AccountID != nil {
		newAccountID = *req.AccountID
	}
	if err := h.repo.AssertRefsExist(c.Request.Context(), familyID, newAccountID, req.CategoryID); err != nil {
		rejectScopedError(c, h.OnDenied, "invalid transaction references", "account/category", err)
		return
	}

	transaction, err := h.repo.UpdateTransaction(c.Request.Context(), familyID, id, updates, req.Version)
	if err != nil {
		if err == repo.ErrOptimisticLock {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		rejectScopedError(c, h.OnDenied, "failed to update transaction", "transaction", err)
		return
	}

	// 更新可能改了 amount_cents 或 account_id，两个账户的余额缓存都要重算：
	//   · 金额变了 -> 原账户余额变；
	//   · 账户换了 -> 原账户（扣回）与新账户（记入）余额都变。
	// 只重算 transaction.AccountID 会在「改账户」时把原账户留在错误的余额上。
	// 余额重算用的是 session 的家庭，不是行里读回来的 family_id。
	for _, acc := range uniqAccounts(before.AccountID, transaction.AccountID) {
		if _, calcErr := h.balanceService.RecalcAccountBalance(c.Request.Context(), acc, familyID); calcErr != nil {
			c.Error(calcErr) //nolint:errcheck // 更新已落库，只记不挡
		}
	}

	c.JSON(http.StatusOK, transaction)
}

// uniqAccounts 去掉空串与重复，保证「重算一次」而不是「对同一账户算两遍」。
func uniqAccounts(ids ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// DeleteTransaction handles DELETE /api/finance/transactions/{id}.
func (h *FinanceHandler) DeleteTransaction(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transaction id is required"})
		return
	}

	// Get current user from context (set by auth middleware) —— 缺 session 直接拒，
	// 不再「拿不到就少判一次」（finance.go 原 507-511 的形状是对的，这里沿用）。
	sess, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	// Load the transaction to check ownership and family_id
	//
	// 「先读后判」只有在读本身带家庭条件时才是安全的：以前 GetTransactionByID 只按 id 查，
	// 于是 isOwner 判的是**调用者自己**的角色 —— A 家庭的 owner 只要角色是 owner，
	// 拿到 B 家庭的行以后就能删（缺陷 C）。现在这一行只可能是本家庭的，
	// 「本家庭的 owner 可以删别人的流水」才是 PRD 15.3 想表达的那件事。
	ctx := c.Request.Context()
	tx, err := h.repo.GetTransactionByID(ctx, familyID, id)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "transaction not found", "transaction", err)
		return
	}

	// Permission check: only author or owner can delete (PRD 15.3)
	isAuthor := tx.CreatedBy != nil && *tx.CreatedBy == sess.MemberID
	isOwner := sess.Role == authz.RoleOwner

	if !isAuthor && !isOwner {
		// 同家庭内的角色不足：这是 403（已认证、无权限），并且按 15.5 落审计。
		deny(c, h.OnDenied, sess, auditEventCrossFamily, "非作者且非 owner，不能删除该流水")
		return
	}

	deletedBy := sess.MemberID

	if err := h.repo.SoftDeleteTransaction(ctx, familyID, id, deletedBy); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to delete transaction", "transaction", err)
		return
	}

	// 软删除改变了 SUM 的分子，余额必须跟着重算（否则账户余额永远停在删除前的值）。
	if _, calcErr := h.balanceService.RecalcAccountBalance(ctx, tx.AccountID, familyID); calcErr != nil {
		c.Error(calcErr) //nolint:errcheck // 流水已软删，只记不挡
	}

	c.JSON(http.StatusOK, gin.H{"message": "transaction deleted successfully"})
}

// ==================== Ledger Handlers ====================

// CreateLedgerRequest represents the request body for creating a ledger.
type CreateLedgerRequest struct {
	FamilyID    string    `json:"family_id" binding:"required,uuid"`
	Name        string    `json:"name" binding:"required"`
	MemberIDs   []string  `json:"member_ids" binding:"required,min=1"`
	PeriodStart time.Time `json:"period_start" binding:"required"`
	PeriodEnd   time.Time `json:"period_end" binding:"required"`
	SortOrder   int32     `json:"sort_order"`
}

// CreateLedger handles POST /api/finance/ledgers.
func (h *FinanceHandler) CreateLedger(c *gin.Context) {
	var req CreateLedgerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	ledger := &model.FinanceLedger{
		FamilyID:    familyID,
		Name:        req.Name,
		MemberIDs:   req.MemberIDs,
		PeriodStart: req.PeriodStart,
		PeriodEnd:   req.PeriodEnd,
		SortOrder:   req.SortOrder,
		Version:     1,
	}

	if err := h.repo.CreateLedger(c.Request.Context(), ledger); err != nil {
		respondRepoError(c, "failed to create ledger", err)
		return
	}

	c.JSON(http.StatusCreated, ledger)
}

// ListLedgers handles GET /api/finance/ledgers.
func (h *FinanceHandler) ListLedgers(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	ledgers, err := h.repo.ListLedgersByFamily(c.Request.Context(), familyID)
	if err != nil {
		respondRepoError(c, "failed to list ledgers", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": ledgers})
}

// getStringValue returns the string value or empty string if nil.
func getStringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ==================== Balance Handlers ====================

// GetAccountBalance handles GET /api/finance/accounts/:id/balance.
//
// 这条路由以前同时带了两个客户端可控输入（路径里的 :id 与 query 里的 family_id），
// SUM 的条件是 `account_id = ? AND family_id = ?` —— 于是只要把 family_id 填成账户
// 真正所属的那个家庭，就能读出别人家庭的账户余额。现在家庭只能来自 session，
// 而账户与家庭不匹配时 SUM 自然为 0，不再由调用方指定配对关系。
func (h *FinanceHandler) GetAccountBalance(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	// 账户归属先确认（不存在或不属于本家庭 → 404，与「id 猜错」同形）。
	if _, err := h.repo.GetAccountByID(c.Request.Context(), familyID, id); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to calculate balance", "account", err)
		return
	}

	balance, err := h.balanceService.CalculateAccountBalance(c.Request.Context(), id, familyID)
	if err != nil {
		respondRepoError(c, "failed to calculate balance", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"account_id": id,
		"balance":    balance,
	})
}

// ==================== Statistics Handlers ====================

// GetOverviewStats handles GET /api/finance/statistics/overview?period=.
func (h *FinanceHandler) GetOverviewStats(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	period := c.Query("period")

	stats, err := h.statisticsService.GetOverview(c.Request.Context(), familyID, period)
	if err != nil {
		// Check if it's a validation error
		if err.Error() != "" && err.Error()[0:7] == "invalid" {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondRepoError(c, "failed to get overview stats", err)
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetTrendStats handles GET /api/finance/statistics/trend?period=&granularity=.
func (h *FinanceHandler) GetTrendStats(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	period := c.Query("period")
	granularity := c.Query("granularity")

	trend, err := h.statisticsService.GetTrend(c.Request.Context(), familyID, period, granularity)
	if err != nil {
		// Check if it's a validation error
		if err.Error() != "" && err.Error()[0:7] == "invalid" {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondRepoError(c, "failed to get trend stats", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": trend})
}

// GetCategoryStats handles GET /api/finance/statistics/category?period=.
func (h *FinanceHandler) GetCategoryStats(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	period := c.Query("period")

	stats, err := h.statisticsService.GetCategoryStats(c.Request.Context(), familyID, period)
	if err != nil {
		// Check if it's a validation error
		if err.Error() != "" && err.Error()[0:7] == "invalid" {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondRepoError(c, "failed to get category stats", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": stats})
}

// GetMemberStats handles GET /api/finance/statistics/member?period=.
func (h *FinanceHandler) GetMemberStats(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	period := c.Query("period")

	stats, err := h.statisticsService.GetMemberStats(c.Request.Context(), familyID, period)
	if err != nil {
		// Check if it's a validation error
		if err.Error() != "" && err.Error()[0:7] == "invalid" {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondRepoError(c, "failed to get member stats", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": stats})
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

// ==================== Budget Handlers ====================

// CreateBudgetRequest represents the request body for creating a budget.
type CreateBudgetRequest struct {
	FamilyID    string    `json:"family_id" binding:"required,uuid"`
	CategoryID  string    `json:"category_id" binding:"required,uuid"`
	AmountCents int64     `json:"amount_cents" binding:"required,min=1"`
	Period      string    `json:"period" binding:"required,oneof=monthly quarterly yearly"`
	StartDate   time.Time `json:"start_date" binding:"required"`
	EndDate     time.Time `json:"end_date" binding:"required"`
}

// CreateBudget handles POST /api/finance/budgets.
func (h *FinanceHandler) CreateBudget(c *gin.Context) {
	var req CreateBudgetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	// budget 的 category_id 与流水的引用同一条道理：只校验 uuid 格式会收进
	// 一个指向别人家分类的预算，预算告警于是打在别人的分类统计上。
	if err := h.repo.AssertRefsExist(c.Request.Context(), familyID, "", &req.CategoryID); err != nil {
		rejectScopedError(c, h.OnDenied, "invalid budget references", "category", err)
		return
	}

	budget := &model.FinanceBudget{
		FamilyID:    familyID,
		CategoryID:  req.CategoryID,
		AmountCents: req.AmountCents,
		Period:      req.Period,
		StartDate:   req.StartDate,
		EndDate:     req.EndDate,
		IsActive:    true,
		Version:     1,
	}

	if err := h.repo.CreateBudget(c.Request.Context(), budget); err != nil {
		respondRepoError(c, "failed to create budget", err)
		return
	}

	c.JSON(http.StatusCreated, budget)
}

// ListBudgets handles GET /api/finance/budgets?period=.
func (h *FinanceHandler) ListBudgets(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	period := c.Query("period")
	var periodPtr *string
	if period != "" {
		periodPtr = &period
	}

	budgets, err := h.repo.ListBudgetsByFamily(c.Request.Context(), familyID, periodPtr)
	if err != nil {
		respondRepoError(c, "failed to list budgets", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": budgets})
}

// ==================== Bill Handlers ====================

// CreateBillRequest represents the request body for creating a bill.
type CreateBillRequest struct {
	FamilyID    string    `json:"family_id" binding:"required,uuid"`
	PayeeID     string    `json:"payee_id" binding:"required,uuid"`
	AmountCents int64     `json:"amount_cents" binding:"required,min=1"`
	DueAt       time.Time `json:"due_at" binding:"required"`
}

// CreateBill handles POST /api/finance/bills.
func (h *FinanceHandler) CreateBill(c *gin.Context) {
	var req CreateBillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	bill := &model.FinanceBill{
		FamilyID:    familyID,
		PayeeID:     req.PayeeID,
		AmountCents: req.AmountCents,
		DueAt:       req.DueAt,
		Status:      "pending",
		Version:     1,
	}

	// The bill row and its finance.due.registered outbox row commit in ONE transaction
	// (BillService.CreateBillWithDueRegistration). PRD 卷首第 3 条 forbids a business surface from
	// keeping its own timer/push path: an 到期日 that is never registered is a due date that never
	// reaches 首页 B 区 / 到期中心. So a failed registration fails the whole write -- returning 201
	// here would leave a bill whose due date silently never got registered.
	if err := h.billService.CreateBillWithDueRegistration(c.Request.Context(), bill); err != nil {
		respondRepoError(c, "failed to create bill", err)
		return
	}

	c.JSON(http.StatusCreated, bill)
}

// ListBills handles GET /api/finance/bills?status=.
func (h *FinanceHandler) ListBills(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	status := c.Query("status")
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}

	bills, err := h.repo.ListBillsByFamily(c.Request.Context(), familyID, statusPtr)
	if err != nil {
		respondRepoError(c, "failed to list bills", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": bills})
}

// PayBill handles PUT /api/finance/bills/:id/pay.
func (h *FinanceHandler) PayBill(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bill id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	// 结清是写操作，并且它会顺带写一条 finance.due.revoked 撤销**对方家庭**的到期注册
	// （writeDueRevokedEvent 用的是行上的 family_id）—— 不带家庭条件的话，
	// 一个猜到的 bill id 就能同时改掉别人的账单与别人的到期提醒。
	bill, err := h.repo.MarkAsPaid(c.Request.Context(), familyID, id)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to mark bill as paid", "bill", err)
		return
	}

	c.JSON(http.StatusOK, bill)
}

// ==================== Export Handlers ====================

// ExportTransactions handles GET /api/finance/export?type=&days=&format=.
//
// 导出是**批量**读，一次就能拿走一个家庭 365 天的全部流水，所以这里的 family_id
// 更不能由客户端给：以前 ?family_id=<别人的家庭> 直接导出一份对方账本（CSV/XLSX）。
// 现在家庭只来自 session，query 里带了就当声明来校验（不一致 403 + 审计）。
func (h *FinanceHandler) ExportTransactions(c *gin.Context) {
	// Get query parameters
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	typeFilter := c.Query("type")
	daysStr := c.Query("days")
	format := c.Query("format")

	// Validate type parameter
	if typeFilter == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type parameter is required (income/expense/transfer/all)"})
		return
	}
	validTypes := map[string]bool{
		"income":   true,
		"expense":  true,
		"transfer": true,
		"all":      true,
	}
	if !validTypes[typeFilter] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid type parameter (must be income, expense, transfer, or all)"})
		return
	}

	// Validate days parameter
	if daysStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days parameter is required (1-365)"})
		return
	}
	var days int
	if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil || days < 1 || days > 365 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid days parameter (must be between 1 and 365)"})
		return
	}

	// Validate format parameter
	if format == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format parameter is required (csv/xlsx)"})
		return
	}
	validFormats := map[string]bool{
		"csv":  true,
		"xlsx": true,
	}
	if !validFormats[format] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid format parameter (must be csv or xlsx)"})
		return
	}

	// Call export service
	data, filename, err := h.exportService.ExportTransactions(c.Request.Context(), familyID, typeFilter, days, format)
	if err != nil {
		respondRepoError(c, "failed to export transactions", err)
		return
	}

	// Set response headers
	contentType := "text/csv"
	if format == "xlsx" {
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	c.Header("Content-Length", fmt.Sprintf("%d", len(data)))

	// Send file data
	c.Data(http.StatusOK, contentType, data)
}

// ==================== Loan Handlers ====================

// CreateLoanRequest represents the request body for creating a loan.
type CreateLoanRequest struct {
	FamilyID       string    `json:"family_id" binding:"required,uuid"`
	LenderName     string    `json:"lender_name" binding:"required"`
	BorrowerName   string    `json:"borrower_name" binding:"required"`
	PrincipalCents int64     `json:"principal_cents" binding:"required,min=1"`
	InterestRate   float64   `json:"interest_rate" binding:"min=0,max=100"`
	StartDate      time.Time `json:"start_date" binding:"required"`
	EndDate        time.Time `json:"end_date" binding:"required"`
}

// CreateLoan handles POST /api/finance/loans.
func (h *FinanceHandler) CreateLoan(c *gin.Context) {
	var req CreateLoanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	loan := &model.FinanceLoan{
		FamilyID:       familyID,
		LenderName:     req.LenderName,
		BorrowerName:   req.BorrowerName,
		PrincipalCents: req.PrincipalCents,
		InterestRate:   req.InterestRate,
		StartDate:      req.StartDate,
		EndDate:        req.EndDate,
		Status:         "active",
		Version:        1,
	}

	if err := h.repo.CreateLoan(c.Request.Context(), loan); err != nil {
		respondRepoError(c, "failed to create loan", err)
		return
	}

	c.JSON(http.StatusCreated, loan)
}

// ListLoans handles GET /api/finance/loans?status=.
func (h *FinanceHandler) ListLoans(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	status := c.Query("status")
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}

	loans, err := h.repo.ListLoansByFamily(c.Request.Context(), familyID, statusPtr)
	if err != nil {
		respondRepoError(c, "failed to list loans", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": loans})
}

// PayOffLoan handles PUT /api/finance/loans/:id/payoff.
func (h *FinanceHandler) PayOffLoan(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "loan id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	loan, err := h.repo.PayOffLoan(c.Request.Context(), familyID, id)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to pay off loan", "loan", err)
		return
	}

	c.JSON(http.StatusOK, loan)
}

// GetRepaymentPlans handles GET /api/finance/loans/:id/repayment-plans.
func (h *FinanceHandler) GetRepaymentPlans(c *gin.Context) {
	loanID := c.Param("id")
	if loanID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "loan id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	plans, err := h.repo.GetRepaymentPlansByLoanID(c.Request.Context(), familyID, loanID)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to get repayment plans", "loan", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": plans})
}

// PayRepaymentPlan handles PUT /api/finance/repayment-plans/:id/pay.
func (h *FinanceHandler) PayRepaymentPlan(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repayment plan id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	plan, err := h.repo.MarkRepaymentPlanAsPaid(c.Request.Context(), familyID, id)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to mark repayment plan as paid", "repayment_plan", err)
		return
	}

	c.JSON(http.StatusOK, plan)
}

// ==================== Goal Handlers ====================

// CreateGoalRequest represents the request body for creating a goal.
type CreateGoalRequest struct {
	FamilyID          string    `json:"family_id" binding:"required,uuid"`
	Name              string    `json:"name" binding:"required"`
	TargetAmountCents int64     `json:"target_amount_cents" binding:"required,min=1"`
	Deadline          time.Time `json:"deadline" binding:"required"`
}

// CreateGoal handles POST /api/finance/goals.
func (h *FinanceHandler) CreateGoal(c *gin.Context) {
	var req CreateGoalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	goal := &model.FinanceGoal{
		FamilyID:           familyID,
		Name:               req.Name,
		TargetAmountCents:  req.TargetAmountCents,
		CurrentAmountCents: 0,
		Deadline:           req.Deadline,
		IsAchieved:         false,
		Version:            1,
	}

	if err := h.repo.CreateGoal(c.Request.Context(), goal); err != nil {
		respondRepoError(c, "failed to create goal", err)
		return
	}

	c.JSON(http.StatusCreated, goal)
}

// ListGoals handles GET /api/finance/goals?family_id=.
func (h *FinanceHandler) ListGoals(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	goals, err := h.repo.ListGoalsByFamily(c.Request.Context(), familyID)
	if err != nil {
		respondRepoError(c, "failed to list goals", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": goals})
}

// UpdateGoalProgress handles PUT /api/finance/goals/:id/progress.
type UpdateGoalProgressRequest struct {
	CurrentAmountCents int64 `json:"current_amount_cents" binding:"required,min=0"`
}

func (h *FinanceHandler) UpdateGoalProgress(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "goal id is required"})
		return
	}

	var req UpdateGoalProgressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	goal, err := h.repo.UpdateGoalProgress(c.Request.Context(), familyID, id, req.CurrentAmountCents)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to update goal progress", "goal", err)
		return
	}

	c.JSON(http.StatusOK, goal)
}

// ==================== Split Settlement Handlers ====================

// CreateSplitSettlementRequest represents the request body for creating a split settlement.
type CreateSplitSettlementRequest struct {
	FamilyID      string `json:"family_id" binding:"required,uuid"`
	TransactionID string `json:"transaction_id" binding:"required,uuid"`
	TotalAmount   int64  `json:"total_amount_cents" binding:"required,ne=0"`
}

// CreateSplitSettlement handles POST /api/finance/split-settlements.
func (h *FinanceHandler) CreateSplitSettlement(c *gin.Context) {
	var req CreateSplitSettlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	// transaction_id 是引用而非路径：必须验证这张流水属于**本家庭**，
	// 否则可以给别人家的账单发起分账。
	if err := h.repo.AssertTransactionsInFamily(c.Request.Context(), familyID, req.TransactionID); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to create split settlement", "transaction", err)
		return
	}

	settlement := &model.FinanceSplitSettlement{
		ID:               generateUUID(),
		FamilyID:         familyID,
		TransactionID:    req.TransactionID,
		Status:           "draft",
		TotalAmountCents: req.TotalAmount,
		Version:          1,
	}

	if err := h.repo.CreateSplitSettlement(c.Request.Context(), settlement); err != nil {
		respondRepoError(c, "failed to create split settlement", err)
		return
	}

	c.JSON(http.StatusCreated, settlement)
}

// ListSplitSettlements handles GET /api/finance/split-settlements?family_id=&status=.
func (h *FinanceHandler) ListSplitSettlements(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	status := c.Query("status")
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}

	settlements, err := h.repo.ListSplitSettlementsByFamily(c.Request.Context(), familyID, statusPtr)
	if err != nil {
		respondRepoError(c, "failed to list split settlements", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": settlements})
}

// AddParticipantRequest represents the request body for adding a participant.
type AddParticipantRequest struct {
	SettlementID     string  `json:"settlement_id" binding:"required,uuid"`
	AccountID        string  `json:"account_id" binding:"required,uuid"`
	ShareRatio       float64 `json:"share_ratio" binding:"required,min=0,max=1"`
	ShareAmountCents int64   `json:"share_amount_cents" binding:"required"`
}

// AddParticipant handles PUT /api/finance/split-settlements/:id/participants.
func (h *FinanceHandler) AddParticipant(c *gin.Context) {
	settlementID := c.Param("id")
	if settlementID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "settlement id is required"})
		return
	}

	var req AddParticipantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	// settlement_id 一律以路径为准（路径 + 家庭共同定位），body 里的同名字段不可信。
	participant := &model.FinanceParticipant{
		ID:               generateUUID(),
		SettlementID:     settlementID,
		AccountID:        req.AccountID,
		ShareRatio:       req.ShareRatio,
		ShareAmountCents: req.ShareAmountCents,
		Status:           "pending",
		Version:          1,
	}

	// account_id 同样是引用：必须属于本家庭。
	if err := h.repo.AssertRefsExist(c.Request.Context(), familyID, req.AccountID, nil); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to add participant", "account", err)
		return
	}

	if err := h.repo.AddParticipant(c.Request.Context(), familyID, participant); err != nil {
		if errors.Is(err, repo.ErrInvalidSplitStatusTransition) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		rejectScopedError(c, h.OnDenied, "failed to add participant", "split settlement", err)
		return
	}

	c.JSON(http.StatusCreated, participant)
}

// SettleSplit handles PUT /api/finance/split-settlements/:id/settle.
func (h *FinanceHandler) SettleSplit(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "settlement id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	settlement, err := h.repo.SettleSplit(c.Request.Context(), familyID, id)
	if err != nil {
		if errors.Is(err, repo.ErrInvalidSplitStatusTransition) || errors.Is(err, repo.ErrSplitAmountMismatch) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		rejectScopedError(c, h.OnDenied, "failed to settle split", "split settlement", err)
		return
	}

	c.JSON(http.StatusOK, settlement)
}

// GetParticipants handles GET /api/finance/split-settlements/:id/participants.
func (h *FinanceHandler) GetParticipants(c *gin.Context) {
	settlementID := c.Param("id")
	if settlementID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "settlement id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	participants, err := h.repo.GetParticipantsBySettlement(c.Request.Context(), familyID, settlementID)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to get participants", "split settlement", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": participants})
}

// ==================== Credit Card Handlers ====================

// CreateCreditCardRequest represents the request body for creating a credit card.
type CreateCreditCardRequest struct {
	FamilyID         string `json:"family_id" binding:"required,uuid"`
	CardNumberHash   string `json:"card_number_hash" binding:"required"`
	Issuer           string `json:"issuer" binding:"required"`
	BillingDay       int32  `json:"billing_day" binding:"required,min=1,max=31"`
	DueDay           int32  `json:"due_day" binding:"required,min=1,max=31"`
	CreditLimitCents int64  `json:"credit_limit_cents" binding:"required,min=1"`
	Currency         string `json:"currency"`
}

// CreateCreditCard handles POST /api/finance/credit-cards.
func (h *FinanceHandler) CreateCreditCard(c *gin.Context) {
	var req CreateCreditCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	card := &model.FinanceCreditCard{
		ID:                  generateUUID(),
		FamilyID:            familyID,
		CardNumberHash:      req.CardNumberHash,
		Issuer:              req.Issuer,
		BillingDay:          req.BillingDay,
		DueDay:              req.DueDay,
		CreditLimitCents:    req.CreditLimitCents,
		CurrentBalanceCents: 0,
		Currency:            req.Currency,
		Status:              "active",
		Version:             1,
	}

	if card.Currency == "" {
		card.Currency = "CNY"
	}

	if err := h.repo.CreateCreditCard(c.Request.Context(), card); err != nil {
		respondRepoError(c, "failed to create credit card", err)
		return
	}

	c.JSON(http.StatusCreated, card)
}

// ListCreditCards handles GET /api/finance/credit-cards?family_id=&status=.
func (h *FinanceHandler) ListCreditCards(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	status := c.Query("status")
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}

	cards, err := h.repo.ListCreditCardsByFamily(c.Request.Context(), familyID, statusPtr)
	if err != nil {
		respondRepoError(c, "failed to list credit cards", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": cards})
}

// UpdateCreditCardBalanceRequest represents the request body for updating credit card balance.
type UpdateCreditCardBalanceRequest struct {
	NewBalanceCents int64 `json:"new_balance_cents" binding:"required,min=0"`
}

// UpdateCreditCardBalance handles PUT /api/finance/credit-cards/:id/balance.
func (h *FinanceHandler) UpdateCreditCardBalance(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "credit card id is required"})
		return
	}

	var req UpdateCreditCardBalanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	card, err := h.repo.UpdateCreditCardBalance(c.Request.Context(), familyID, id, req.NewBalanceCents)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to update credit card balance", "credit card", err)
		return
	}

	c.JSON(http.StatusOK, card)
}

// ==================== Invoice Handlers ====================

// CreateInvoiceRequest represents the request body for creating an invoice.
type CreateInvoiceRequest struct {
	FamilyID       string    `json:"family_id" binding:"required,uuid"`
	InvoiceNumber  string    `json:"invoice_number" binding:"required"`
	AmountCents    int64     `json:"amount_cents" binding:"required,min=0"`
	TaxAmountCents int64     `json:"tax_amount_cents"`
	Vendor         string    `json:"vendor" binding:"required"`
	IssueDate      time.Time `json:"issue_date" binding:"required"`
}

// CreateInvoice handles POST /api/finance/invoices.
func (h *FinanceHandler) CreateInvoice(c *gin.Context) {
	var req CreateInvoiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	invoice := &model.FinanceInvoice{
		ID:                  generateUUID(),
		FamilyID:            familyID,
		InvoiceNumber:       req.InvoiceNumber,
		AmountCents:         req.AmountCents,
		TaxAmountCents:      req.TaxAmountCents,
		Vendor:              req.Vendor,
		IssueDate:           req.IssueDate,
		ReimbursementStatus: "pending",
		Version:             1,
	}

	if err := h.repo.CreateInvoice(c.Request.Context(), invoice); err != nil {
		respondRepoError(c, "failed to create invoice", err)
		return
	}

	c.JSON(http.StatusCreated, invoice)
}

// ListInvoices handles GET /api/finance/invoices?family_id=&status=.
func (h *FinanceHandler) ListInvoices(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	status := c.Query("status")
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}

	invoices, err := h.repo.ListInvoicesByFamily(c.Request.Context(), familyID, statusPtr)
	if err != nil {
		respondRepoError(c, "failed to list invoices", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": invoices})
}

// ReimburseInvoiceRequest represents the request body for reimbursing an invoice.
type ReimburseInvoiceRequest struct {
	Status        string  `json:"status" binding:"required,oneof=reimbursed rejected"`
	Reason        *string `json:"reason"`
	TransactionID *string `json:"transaction_id"`
}

// ReimburseInvoice handles PUT /api/finance/invoices/:id/reimburse.
func (h *FinanceHandler) ReimburseInvoice(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invoice id is required"})
		return
	}

	var req ReimburseInvoiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	// 凭证可以挂一张流水（transaction_id）：这同样是跨资源引用，必须属于本家庭，
	// 否则可以把别人家的支出认领成自己的报销凭证。
	invoice, err := h.repo.MarkInvoiceAsReimbursed(c.Request.Context(), familyID, id, req.Status, req.Reason, req.TransactionID)
	if err != nil {
		if errors.Is(err, repo.ErrInvalidInvoiceStatusTransition) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		rejectScopedError(c, h.OnDenied, "failed to update invoice status", "invoice", err)
		return
	}

	c.JSON(http.StatusOK, invoice)
}

// ==================== Asset-Liability Report Handlers ====================

// GenerateAssetLiabilityReportRequest represents the request body for generating a report.
type GenerateAssetLiabilityReportRequest struct {
	FamilyID string `json:"family_id" binding:"required,uuid"`
	Period   string `json:"period" binding:"required"` // YYYY-MM format
}

// GenerateAssetLiabilityReport handles POST /api/finance/reports/asset-liability.
func (h *FinanceHandler) GenerateAssetLiabilityReport(c *gin.Context) {
	var req GenerateAssetLiabilityReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	report, err := h.repo.GenerateAssetLiabilityReport(c.Request.Context(), familyID, req.Period)
	if err != nil {
		if errors.Is(err, repo.ErrAssetLiabilityMismatch) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		rejectScopedError(c, h.OnDenied, "failed to generate asset-liability report", "report", err)
		return
	}

	c.JSON(http.StatusCreated, report)
}

// GetAssetLiabilityReport handles GET /api/finance/reports/asset-liability?family_id=&period=.
func (h *FinanceHandler) GetAssetLiabilityReport(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	period := c.Query("period")

	var report *model.FinanceAssetLiabilityReport
	var err error

	if period != "" {
		report, err = h.repo.GetAssetLiabilityReportByPeriod(c.Request.Context(), familyID, period)
	} else {
		report, err = h.repo.GetLatestAssetLiabilityReport(c.Request.Context(), familyID)
	}

	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to get asset-liability report", "report", err)
		return
	}

	if report == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no asset-liability report found"})
		return
	}

	c.JSON(http.StatusOK, report)
}

// ==================== Tag Handlers ====================

// CreateTagRequest represents the request body for creating a tag.
type CreateTagRequest struct {
	FamilyID string `json:"family_id" binding:"required,uuid"`
	Name     string `json:"name" binding:"required,max=50"`
	Color    string `json:"color" binding:"omitempty"` // hex color like #FF5733
}

// CreateTag handles POST /api/finance/tags.
func (h *FinanceHandler) CreateTag(c *gin.Context) {
	var req CreateTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	tag := &model.FinanceTag{
		FamilyID: familyID,
		Name:     req.Name,
		Color:    req.Color,
	}

	if err := h.repo.CreateTag(c.Request.Context(), tag); err != nil {
		respondRepoError(c, "failed to create tag", err)
		return
	}

	c.JSON(http.StatusCreated, tag)
}

// ListTags handles GET /api/finance/tags?family_id=.
func (h *FinanceHandler) ListTags(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	tags, err := h.repo.ListTagsByFamily(c.Request.Context(), familyID)
	if err != nil {
		respondRepoError(c, "failed to list tags", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": tags})
}

// UpdateTagRequest represents the request body for updating a tag.
type UpdateTagRequest struct {
	Name  string `json:"name" binding:"omitempty,max=50"`
	Color string `json:"color" binding:"omitempty"`
}

// UpdateTag handles PUT /api/finance/tags/:id.
func (h *FinanceHandler) UpdateTag(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tag id is required"})
		return
	}

	var req UpdateTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	// 这里的 familyID 来自 session：以前它是 c.Query("family_id")，
	// 于是「路径 id + 客户端给的 family_id」这一对可以由攻击者任意配对，
	// 拿 A 家庭的 token 传 family_id=B & id=<B 的标签> 就能改写 B 家的标签。
	tag, err := h.repo.GetTagByID(c.Request.Context(), familyID, id)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to get tag", "tag", err)
		return
	}

	if tag == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "tag not found"})
		return
	}

	if req.Name != "" {
		tag.Name = req.Name
	}
	if req.Color != "" {
		tag.Color = req.Color
	}

	if err := h.repo.UpdateTag(c.Request.Context(), tag); err != nil {
		respondRepoError(c, "failed to update tag", err)
		return
	}

	c.JSON(http.StatusOK, tag)
}

// DeleteTag handles DELETE /api/finance/tags/:id.
func (h *FinanceHandler) DeleteTag(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tag id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	if err := h.repo.DeleteTag(c.Request.Context(), familyID, id); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to delete tag", "tag", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "tag deleted successfully"})
}

// ==================== Recurring Rule Handlers ====================

// ListRecurringRules handles GET /api/finance/recurring?family_id=.
func (h *FinanceHandler) ListRecurringRules(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	rules, err := h.repo.ListRecurringRulesByFamily(c.Request.Context(), familyID)
	if err != nil {
		respondRepoError(c, "failed to list recurring rules", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": rules})
}

// CreateRecurringRule handles POST /api/finance/recurring.
func (h *FinanceHandler) CreateRecurringRule(c *gin.Context) {
	var req struct {
		FamilyID    string     `json:"family_id" binding:"required,uuid"`
		Name        string     `json:"name" binding:"required"`
		Type        string     `json:"type" binding:"required,oneof=expense income"`
		AmountCents int64      `json:"amount_cents" binding:"required"`
		AccountID   string     `json:"account_id" binding:"required,uuid"`
		CategoryID  string     `json:"category_id" binding:"required,uuid"`
		Cycle       string     `json:"cycle" binding:"required,oneof=daily weekly monthly yearly"`
		StartDate   time.Time  `json:"start_date" binding:"required"`
		EndDate     *time.Time `json:"end_date,omitempty"`
		Description string     `json:"description,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	// 周期规则到点会替客户记账，所以 account_id / category_id 必须是**本家庭**的资源，
	// 否则别人家的账户会被这条规则持续写入。
	if err := h.repo.AssertRefsExist(c.Request.Context(), familyID, req.AccountID, &req.CategoryID); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to create recurring rule", "account", err)
		return
	}

	rule := &model.FinanceRecurringRule{
		FamilyID:      familyID,
		Name:          req.Name,
		Type:          req.Type,
		AmountCents:   req.AmountCents,
		AccountID:     req.AccountID,
		CategoryID:    req.CategoryID,
		Cycle:         req.Cycle,
		StartDate:     req.StartDate,
		EndDate:       req.EndDate,
		NextExecuteAt: req.StartDate,
		IsActive:      true,
		Description:   req.Description,
	}

	if err := h.repo.CreateRecurringRule(c.Request.Context(), rule); err != nil {
		respondRepoError(c, "failed to create recurring rule", err)
		return
	}

	c.JSON(http.StatusCreated, rule)
}

// UpdateRecurringRule handles PUT /api/finance/recurring/:id.
func (h *FinanceHandler) UpdateRecurringRule(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "recurring rule id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	rule, err := h.repo.GetRecurringRuleByID(c.Request.Context(), familyID, id)
	if err != nil {
		rejectScopedError(c, h.OnDenied, "failed to get recurring rule", "recurring rule", err)
		return
	}
	if rule == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recurring rule not found"})
		return
	}

	var req struct {
		Name        string     `json:"name,omitempty"`
		Type        string     `json:"type,omitempty" binding:"omitempty,oneof=expense income"`
		AmountCents int64      `json:"amount_cents,omitempty"`
		AccountID   string     `json:"account_id,omitempty" binding:"omitempty,uuid"`
		CategoryID  string     `json:"category_id,omitempty" binding:"omitempty,uuid"`
		Cycle       string     `json:"cycle,omitempty" binding:"omitempty,oneof=daily weekly monthly yearly"`
		StartDate   *time.Time `json:"start_date,omitempty"`
		EndDate     *time.Time `json:"end_date,omitempty"`
		IsActive    *bool      `json:"is_active,omitempty"`
		Description string     `json:"description,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	// 改引用同样要过家庭校验：把 account_id/category_id 换成别家的 id 之后，
	// 这条规则后续每次执行都会往别人家的账本里写流水。
	if req.AccountID != "" || req.CategoryID != "" {
		if err := h.repo.AssertRefsExist(c.Request.Context(), familyID, req.AccountID, &req.CategoryID); err != nil {
			rejectScopedError(c, h.OnDenied, "failed to update recurring rule", "account", err)
			return
		}
	}

	if req.Name != "" {
		rule.Name = req.Name
	}
	if req.Type != "" {
		rule.Type = req.Type
	}
	if req.AmountCents != 0 {
		rule.AmountCents = req.AmountCents
	}
	if req.AccountID != "" {
		rule.AccountID = req.AccountID
	}
	if req.CategoryID != "" {
		rule.CategoryID = req.CategoryID
	}
	if req.Cycle != "" {
		rule.Cycle = req.Cycle
	}
	if req.StartDate != nil {
		rule.StartDate = *req.StartDate
	}
	if req.EndDate != nil {
		rule.EndDate = req.EndDate
	}
	if req.IsActive != nil {
		rule.IsActive = *req.IsActive
	}
	if req.Description != "" {
		rule.Description = req.Description
	}

	if err := h.repo.UpdateRecurringRule(c.Request.Context(), rule); err != nil {
		respondRepoError(c, "failed to update recurring rule", err)
		return
	}

	c.JSON(http.StatusOK, rule)
}

// DeleteRecurringRule handles DELETE /api/finance/recurring/:id.
func (h *FinanceHandler) DeleteRecurringRule(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "recurring rule id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	if err := h.repo.DeleteRecurringRule(c.Request.Context(), familyID, id); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to delete recurring rule", "recurring rule", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "recurring rule deleted successfully"})
}

// ==================== Trash Handlers ====================

// ListTrash handles GET /api/finance/trash?family_id=&type=.
func (h *FinanceHandler) ListTrash(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	trashType := c.Query("type") // Optional filter: transaction, category, account, etc.

	var items []repo.DeletedTransaction
	var err error

	// For now, only support transaction trash
	if trashType == "" || trashType == "transaction" {
		items, err = h.repo.ListDeletedTransactions(c.Request.Context(), familyID)
		if err != nil {
			respondRepoError(c, "failed to list trash", err)
			return
		}
	} else {
		// Other types not yet implemented
		items = []repo.DeletedTransaction{}
	}

	c.JSON(http.StatusOK, gin.H{"items": items})
}

// RestoreTrashItem handles POST /api/finance/trash/:id/restore.
func (h *FinanceHandler) RestoreTrashItem(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trash item id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	if err := h.repo.RestoreTransaction(c.Request.Context(), id, familyID); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to restore item", "trash item", err)
		return
	}

	// After restoration, recalculate account balance
	// The restored transaction's amount needs to be added back to the account balance
	transaction, err := h.repo.GetTransactionByID(c.Request.Context(), familyID, id)
	if err == nil && transaction != nil && transaction.AccountID != "" {
		// Recalculate balance for the affected account
		_, calcErr := h.balanceService.CalculateAccountBalance(c.Request.Context(), transaction.AccountID, familyID)
		if calcErr != nil {
			// Log error but don't fail the restore operation
			// In production, this would trigger an async job
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "item restored successfully"})
}

// PermanentlyDeleteTrashItem handles DELETE /api/finance/trash/:id.
func (h *FinanceHandler) PermanentlyDeleteTrashItem(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trash item id is required"})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	if err := h.repo.PermanentlyDeleteTransaction(c.Request.Context(), id, familyID); err != nil {
		rejectScopedError(c, h.OnDenied, "failed to permanently delete item", "trash item", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "item permanently deleted"})
}

// ClearExpiredTrash handles POST /api/finance/trash/clear-expired.
func (h *FinanceHandler) ClearExpiredTrash(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	count, err := h.repo.ClearExpiredTrash(c.Request.Context(), familyID)
	if err != nil {
		respondRepoError(c, "failed to clear expired trash", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "expired trash cleared",
		"count":   count,
	})
}

// ==================== Finance Settings Handlers ====================

// GetFinanceSettings handles GET /api/finance/settings?family_id=.
func (h *FinanceHandler) GetFinanceSettings(c *gin.Context) {
	_, familyID, ok := scopeFamily(c, h.OnDenied, c.Query("family_id"))
	if !ok {
		return
	}

	settings, err := h.repo.GetSettingsByFamily(c.Request.Context(), familyID)
	if err != nil {
		respondRepoError(c, "failed to get finance settings", err)
		return
	}

	// If no settings exist, create defaults
	if settings == nil {
		settings, err = h.repo.CreateDefaultSettings(c.Request.Context(), familyID)
		if err != nil {
			respondRepoError(c, "failed to create default settings", err)
			return
		}
	}

	c.JSON(http.StatusOK, settings)
}

// UpdateFinanceSettingsRequest represents the request body for updating finance settings.
type UpdateFinanceSettingsRequest struct {
	FamilyID              string   `json:"family_id" binding:"required,uuid"`
	CurrencyUnit          *string  `json:"currency_unit,omitempty" binding:"omitempty,oneof=CNY USD EUR JPY GBP"`
	DecimalPlaces         *int32   `json:"decimal_places,omitempty" binding:"omitempty,min=0,max=4"`
	BudgetAlertThreshold  *float64 `json:"budget_alert_threshold,omitempty" binding:"omitempty,min=0.1,max=1.0"`
	AutoCategorizeEnabled *bool    `json:"auto_categorize_enabled,omitempty"`
	ReceiptOCREnabled     *bool    `json:"receipt_ocr_enabled,omitempty"`
	VoiceInputEnabled     *bool    `json:"voice_input_enabled,omitempty"`
}

// UpdateFinanceSettings handles PUT /api/finance/settings.
func (h *FinanceHandler) UpdateFinanceSettings(c *gin.Context) {
	var req UpdateFinanceSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	_, familyID, ok := scopeFamily(c, h.OnDenied, req.FamilyID)
	if !ok {
		return
	}

	// Get existing settings or create defaults —— 一律按 session 家庭读写，
	// body 的 family_id 只用于越权检测（见 packages/auth 与 scope.go）。
	settings, err := h.repo.GetSettingsByFamily(c.Request.Context(), familyID)
	if err != nil {
		respondRepoError(c, "failed to get finance settings", err)
		return
	}

	if settings == nil {
		settings, err = h.repo.CreateDefaultSettings(c.Request.Context(), familyID)
		if err != nil {
			respondRepoError(c, "failed to create default settings", err)
			return
		}
	}

	// Apply updates only for non-nil fields
	if req.CurrencyUnit != nil {
		settings.CurrencyUnit = *req.CurrencyUnit
	}
	if req.DecimalPlaces != nil {
		settings.DecimalPlaces = *req.DecimalPlaces
	}
	if req.BudgetAlertThreshold != nil {
		settings.BudgetAlertThreshold = *req.BudgetAlertThreshold
	}
	if req.AutoCategorizeEnabled != nil {
		settings.AutoCategorizeEnabled = *req.AutoCategorizeEnabled
	}
	if req.ReceiptOCREnabled != nil {
		settings.ReceiptOCREnabled = *req.ReceiptOCREnabled
	}
	if req.VoiceInputEnabled != nil {
		settings.VoiceInputEnabled = *req.VoiceInputEnabled
	}

	// Save updated settings
	if err := h.repo.UpsertSettings(c.Request.Context(), settings); err != nil {
		respondRepoError(c, "failed to update finance settings", err)
		return
	}

	c.JSON(http.StatusOK, settings)
}
