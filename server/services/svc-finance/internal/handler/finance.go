// Package handler provides HTTP handlers for the finance service.
package handler

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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
}

// NewFinanceHandler creates a new finance handler instance.
func NewFinanceHandler(repo *repo.FinanceRepo, balanceService *service.BalanceService, statisticsService *service.StatisticsService, budgetAlertService *service.BudgetAlertService, exportService *service.ExportService) *FinanceHandler {
	return &FinanceHandler{
		repo:               repo,
		balanceService:     balanceService,
		statisticsService:  statisticsService,
		budgetAlertService: budgetAlertService,
		exportService:      exportService,
	}
}

// ==================== Account Handlers ====================

// CreateAccount handles POST /api/finance/accounts.
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

	account := &model.FinanceAccount{
		FamilyID: req.FamilyID,
		Name:     req.Name,
		Type:     req.Type,
		Balance:  0,
		Version:  1,
	}

	if err := h.repo.CreateAccount(c.Request.Context(), account); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create account: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, account)
}

// ListAccounts handles GET /api/finance/accounts?family_id=.
func (h *FinanceHandler) ListAccounts(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

	accounts, err := h.repo.ListAccountsByFamily(c.Request.Context(), familyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list accounts: " + err.Error()})
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

	if err := h.repo.ArchiveAccount(c.Request.Context(), id); err != nil {
		if err == repo.ErrAccountBalanceNonZero {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to archive account: " + err.Error()})
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

	category := &model.FinanceCategory{
		FamilyID:  req.FamilyID,
		Name:      req.Name,
		Icon:      req.Icon,
		SortOrder: req.SortOrder,
		IsActive:  true,
		Version:   1,
	}

	if err := h.repo.CreateCategory(c.Request.Context(), category); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create category: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, category)
}

// ListCategories handles GET /api/finance/categories?family_id=&is_active=.
func (h *FinanceHandler) ListCategories(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list categories: " + err.Error()})
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

	if err := h.repo.DeactivateCategory(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to deactivate category: " + err.Error()})
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
func (h *FinanceHandler) CreateTransaction(c *gin.Context) {
	var req CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	// Validate amount based on type
	if req.Type == "expense" && req.AmountCents > 0 {
		req.AmountCents = -req.AmountCents // Make expense amounts negative
	} else if req.Type == "income" && req.AmountCents < 0 {
		req.AmountCents = -req.AmountCents // Make income amounts positive
	}

	transaction := &model.FinanceTransaction{
		ID:              generateUUID(),
		FamilyID:        req.FamilyID,
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

	input := repo.CreateTransactionInput{
		Transaction: transaction,
		ClientReqID: getStringValue(req.ClientRequestID),
		FamilyID:    req.FamilyID,
		RequestData: req,
	}

	if err := h.repo.CreateTransaction(c.Request.Context(), input); err != nil {
		if err == repo.ErrDuplicateRequest {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create transaction: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, transaction)
}

// ListTransactions handles GET /api/finance/transactions?family_id=&period=&cursor=.
func (h *FinanceHandler) ListTransactions(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

	period := c.Query("period")
	cursor := c.Query("cursor")
	limit := 50

	transactions, nextCursor, err := h.repo.ListTransactionsByFamily(c.Request.Context(), familyID, period, &cursor, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list transactions: " + err.Error()})
		return
	}

	response := gin.H{
		"items": transactions,
	}
	if nextCursor != nil {
		response["next_cursor"] = *nextCursor
	}

	c.JSON(http.StatusOK, response)
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

	transaction, err := h.repo.UpdateTransaction(c.Request.Context(), id, updates, req.Version)
	if err != nil {
		if err == repo.ErrOptimisticLock {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update transaction: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, transaction)
}

// DeleteTransaction handles DELETE /api/finance/transactions/{id}.
func (h *FinanceHandler) DeleteTransaction(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transaction id is required"})
		return
	}

	// In a real implementation, deletedBy would come from the authenticated user context
	deletedBy := "system" // Placeholder

	if err := h.repo.SoftDeleteTransaction(c.Request.Context(), id, deletedBy); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete transaction: " + err.Error()})
		return
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

	ledger := &model.FinanceLedger{
		FamilyID:    req.FamilyID,
		Name:        req.Name,
		MemberIDs:   req.MemberIDs,
		PeriodStart: req.PeriodStart,
		PeriodEnd:   req.PeriodEnd,
		SortOrder:   req.SortOrder,
		Version:     1,
	}

	if err := h.repo.CreateLedger(c.Request.Context(), ledger); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create ledger: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, ledger)
}

// ListLedgers handles GET /api/finance/ledgers?family_id=.
func (h *FinanceHandler) ListLedgers(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

	ledgers, err := h.repo.ListLedgersByFamily(c.Request.Context(), familyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list ledgers: " + err.Error()})
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

// GetAccountBalance handles GET /api/finance/accounts/:id/balance?family_id=.
func (h *FinanceHandler) GetAccountBalance(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account id is required"})
		return
	}

	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

	balance, err := h.balanceService.CalculateAccountBalance(c.Request.Context(), id, familyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to calculate balance: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"account_id": id,
		"balance":    balance,
	})
}

// ==================== Statistics Handlers ====================

// GetOverviewStats handles GET /api/finance/statistics/overview?family_id=&period=.
func (h *FinanceHandler) GetOverviewStats(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get overview stats: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetTrendStats handles GET /api/finance/statistics/trend?family_id=&period=&granularity=.
func (h *FinanceHandler) GetTrendStats(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get trend stats: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": trend})
}

// GetCategoryStats handles GET /api/finance/statistics/category?family_id=&period=.
func (h *FinanceHandler) GetCategoryStats(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get category stats: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": stats})
}

// GetMemberStats handles GET /api/finance/statistics/member?family_id=&period=.
func (h *FinanceHandler) GetMemberStats(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get member stats: " + err.Error()})
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

	budget := &model.FinanceBudget{
		FamilyID:    req.FamilyID,
		CategoryID:  req.CategoryID,
		AmountCents: req.AmountCents,
		Period:      req.Period,
		StartDate:   req.StartDate,
		EndDate:     req.EndDate,
		IsActive:    true,
		Version:     1,
	}

	if err := h.repo.CreateBudget(c.Request.Context(), budget); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create budget: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, budget)
}

// ListBudgets handles GET /api/finance/budgets?family_id=&period=.
func (h *FinanceHandler) ListBudgets(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

	period := c.Query("period")
	var periodPtr *string
	if period != "" {
		periodPtr = &period
	}

	budgets, err := h.repo.ListBudgetsByFamily(c.Request.Context(), familyID, periodPtr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list budgets: " + err.Error()})
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

	bill := &model.FinanceBill{
		FamilyID:    req.FamilyID,
		PayeeID:     req.PayeeID,
		AmountCents: req.AmountCents,
		DueAt:       req.DueAt,
		Status:      "pending",
		Version:     1,
	}

	if err := h.repo.CreateBill(c.Request.Context(), bill); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create bill: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, bill)
}

// ListBills handles GET /api/finance/bills?family_id=&status=.
func (h *FinanceHandler) ListBills(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

	status := c.Query("status")
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}

	bills, err := h.repo.ListBillsByFamily(c.Request.Context(), familyID, statusPtr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list bills: " + err.Error()})
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

	bill, err := h.repo.MarkAsPaid(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark bill as paid: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, bill)
}

// ==================== Export Handlers ====================

// ExportTransactions handles GET /api/finance/export?type=&days=&format=.
func (h *FinanceHandler) ExportTransactions(c *gin.Context) {
	// Get query parameters
	familyID := c.Query("family_id")
	typeFilter := c.Query("type")
	daysStr := c.Query("days")
	format := c.Query("format")

	// Validate family_id
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to export transactions: " + err.Error()})
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

	loan := &model.FinanceLoan{
		FamilyID:       req.FamilyID,
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create loan: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, loan)
}

// ListLoans handles GET /api/finance/loans?family_id=&status=.
func (h *FinanceHandler) ListLoans(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

	status := c.Query("status")
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}

	loans, err := h.repo.ListLoansByFamily(c.Request.Context(), familyID, statusPtr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list loans: " + err.Error()})
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

	loan, err := h.repo.PayOffLoan(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to pay off loan: " + err.Error()})
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

	plans, err := h.repo.GetRepaymentPlansByLoanID(c.Request.Context(), loanID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get repayment plans: " + err.Error()})
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

	plan, err := h.repo.MarkRepaymentPlanAsPaid(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark repayment plan as paid: " + err.Error()})
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

	goal := &model.FinanceGoal{
		FamilyID:           req.FamilyID,
		Name:               req.Name,
		TargetAmountCents:  req.TargetAmountCents,
		CurrentAmountCents: 0,
		Deadline:           req.Deadline,
		IsAchieved:         false,
		Version:            1,
	}

	if err := h.repo.CreateGoal(c.Request.Context(), goal); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create goal: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, goal)
}

// ListGoals handles GET /api/finance/goals?family_id=.
func (h *FinanceHandler) ListGoals(c *gin.Context) {
	familyID := c.Query("family_id")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "family_id is required"})
		return
	}

	goals, err := h.repo.ListGoalsByFamily(c.Request.Context(), familyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list goals: " + err.Error()})
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

	goal, err := h.repo.UpdateGoalProgress(c.Request.Context(), id, req.CurrentAmountCents)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update goal progress: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, goal)
}
