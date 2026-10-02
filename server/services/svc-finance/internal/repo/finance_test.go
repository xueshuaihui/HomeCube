package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestRepo(t *testing.T) (*repo.FinanceRepo, *gorm.DB) {
	t.Helper()

	// Set table prefix to empty for SQLite testing
	model.TablePrefix = ""

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Enable WAL mode and foreign keys for SQLite
	db.Exec("PRAGMA journal_mode=WAL;")
	db.Exec("PRAGMA foreign_keys=ON;")

	// Create change_log and idempotency tables that sync.Repo expects
	db.Exec(`CREATE TABLE IF NOT EXISTS finance_change_log (
		lsn INTEGER PRIMARY KEY AUTOINCREMENT,
		family_id TEXT NOT NULL,
		entity TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		op TEXT NOT NULL,
		version INTEGER NOT NULL,
		data BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)

	db.Exec(`CREATE TABLE IF NOT EXISTS finance_idempotency (
		key TEXT PRIMARY KEY,
		family_id TEXT NOT NULL,
		request_hash TEXT NOT NULL,
		response_snapshot BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)

	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uk_request_hash ON finance_idempotency(request_hash)")
	db.Exec("CREATE INDEX IF NOT EXISTS idx_family_lsn ON finance_change_log(family_id, lsn)")

	// Migrate all finance models
	err = db.AutoMigrate(
		&model.FinanceAccount{},
		&model.FinanceCategory{},
		&model.FinanceTransaction{},
		&model.FinanceLedger{},
		&model.FinanceBudget{},
		&model.FinanceBill{},
		&model.FinanceLoan{},
		&model.FinanceRepaymentPlan{},
		&model.FinanceGoal{},
	)
	if err != nil {
		t.Fatalf("failed to migrate models: %v", err)
	}

	r := repo.NewFinanceRepo(db)
	return r, db
}

func TestCreateAndGetAccount(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	account := &model.FinanceAccount{
		FamilyID: "test-family-001",
		Name:     "Test Account",
		Type:     "bank",
		Balance:  100000,
		Version:  1,
	}

	err := r.CreateAccount(ctx, account)
	if err != nil {
		t.Fatalf("failed to create account: %v", err)
	}

	retrieved, err := r.GetAccountByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("failed to get account: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected account to be retrieved")
	}

	if retrieved.Name != account.Name {
		t.Errorf("expected name %s, got %s", account.Name, retrieved.Name)
	}
}

func TestListAccountsByFamily(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create multiple accounts
	for i := 0; i < 3; i++ {
		account := &model.FinanceAccount{
			FamilyID: "test-family-001",
			Name:     "Account " + string(rune('A'+i)),
			Type:     "bank",
			Balance:  int64((i + 1) * 10000),
			Version:  1,
		}
		err := r.CreateAccount(ctx, account)
		if err != nil {
			t.Fatalf("failed to create account %d: %v", i, err)
		}
	}

	accounts, err := r.ListAccountsByFamily(ctx, "test-family-001")
	if err != nil {
		t.Fatalf("failed to list accounts: %v", err)
	}

	if len(accounts) != 3 {
		t.Errorf("expected 3 accounts, got %d", len(accounts))
	}
}

func TestArchiveAccountWithZeroBalance(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	account := &model.FinanceAccount{
		FamilyID: "test-family-001",
		Name:     "Empty Account",
		Type:     "cash",
		Balance:  0,
		Version:  1,
	}

	err := r.CreateAccount(ctx, account)
	if err != nil {
		t.Fatalf("failed to create account: %v", err)
	}

	err = r.ArchiveAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("failed to archive account with zero balance: %v", err)
	}

	retrieved, err := r.GetAccountByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("failed to get account: %v", err)
	}

	if !retrieved.IsArchived {
		t.Error("expected account to be archived")
	}
}

func TestArchiveAccountWithNonZeroBalance(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	account := &model.FinanceAccount{
		FamilyID: "test-family-001",
		Name:     "Non-Empty Account",
		Type:     "bank",
		Balance:  50000,
		Version:  1,
	}

	err := r.CreateAccount(ctx, account)
	if err != nil {
		t.Fatalf("failed to create account: %v", err)
	}

	err = r.ArchiveAccount(ctx, account.ID)
	if err != repo.ErrAccountBalanceNonZero {
		t.Errorf("expected ErrAccountBalanceNonZero, got %v", err)
	}
}

func TestCreateAndGetCategory(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	category := &model.FinanceCategory{
		FamilyID:  "test-family-001",
		Name:      "Food",
		Icon:      "🍔",
		SortOrder: 1,
		IsActive:  true,
		Version:   1,
	}

	err := r.CreateCategory(ctx, category)
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	retrieved, err := r.GetCategoryByID(ctx, category.ID)
	if err != nil {
		t.Fatalf("failed to get category: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected category to be retrieved")
	}

	if retrieved.Name != category.Name {
		t.Errorf("expected name %s, got %s", category.Name, retrieved.Name)
	}
}

func TestDeactivateCategory(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	category := &model.FinanceCategory{
		FamilyID:  "test-family-001",
		Name:      "Old Category",
		IsActive:  true,
		SortOrder: 1,
		Version:   1,
	}

	err := r.CreateCategory(ctx, category)
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	err = r.DeactivateCategory(ctx, category.ID)
	if err != nil {
		t.Fatalf("failed to deactivate category: %v", err)
	}

	retrieved, err := r.GetCategoryByID(ctx, category.ID)
	if err != nil {
		t.Fatalf("failed to get category: %v", err)
	}

	if retrieved.IsActive {
		t.Error("expected category to be deactivated")
	}
}

func TestCreateAndGetTransaction(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	now := time.Now()
	transaction := &model.FinanceTransaction{
		FamilyID:    "test-family-001",
		Type:        "expense",
		AmountCents: -5000,
		AccountID:   "test-account-001",
		OccurredAt:  now,
		Description: "Lunch",
		Version:     1,
	}

	input := repo.CreateTransactionInput{
		Transaction: transaction,
		FamilyID:    "test-family-001",
		RequestData: transaction,
	}

	err := r.CreateTransaction(ctx, input)
	if err != nil {
		t.Fatalf("failed to create transaction: %v", err)
	}

	retrieved, err := r.GetTransactionByID(ctx, transaction.ID)
	if err != nil {
		t.Fatalf("failed to get transaction: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected transaction to be retrieved")
	}

	if retrieved.AmountCents != transaction.AmountCents {
		t.Errorf("expected amount %d, got %d", transaction.AmountCents, retrieved.AmountCents)
	}
}

func TestIdempotentTransaction(t *testing.T) {
	t.Skip("Idempotency test requires proper transaction isolation in SQLite")

	r, _ := setupTestRepo(t)
	ctx := context.Background()

	now := time.Now()
	clientReqID := "client-req-001"

	transaction := &model.FinanceTransaction{
		FamilyID:        "test-family-001",
		Type:            "expense",
		AmountCents:     -3000,
		AccountID:       "test-account-001",
		OccurredAt:      now,
		Description:     "Coffee",
		ClientRequestID: &clientReqID,
		Version:         1,
	}

	input := repo.CreateTransactionInput{
		Transaction: transaction,
		ClientReqID: clientReqID,
		FamilyID:    "test-family-001",
		RequestData: transaction,
	}

	// First creation should succeed
	err := r.CreateTransaction(ctx, input)
	if err != nil {
		t.Fatalf("failed to create first transaction: %v", err)
	}

	// Second creation with same client_request_id should fail
	transaction2 := &model.FinanceTransaction{
		FamilyID:        "test-family-001",
		Type:            "expense",
		AmountCents:     -3000,
		AccountID:       "test-account-001",
		OccurredAt:      now,
		Description:     "Coffee Duplicate",
		ClientRequestID: &clientReqID,
		Version:         1,
	}

	input2 := repo.CreateTransactionInput{
		Transaction: transaction2,
		ClientReqID: clientReqID,
		FamilyID:    "test-family-001",
		RequestData: transaction2,
	}

	err = r.CreateTransaction(ctx, input2)
	if err != repo.ErrDuplicateRequest {
		t.Errorf("expected ErrDuplicateRequest, got %v", err)
	}
}

func TestListTransactionsWithCursor(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create multiple transactions
	for i := 0; i < 5; i++ {
		transaction := &model.FinanceTransaction{
			FamilyID:    "test-family-001",
			Type:        "expense",
			AmountCents: -int64((i + 1) * 1000),
			AccountID:   "test-account-001",
			OccurredAt:  time.Now().Add(-time.Duration(i) * time.Hour),
			Description: "Transaction " + string(rune('A'+i)),
			Version:     1,
		}

		input := repo.CreateTransactionInput{
			Transaction: transaction,
			FamilyID:    "test-family-001",
			RequestData: transaction,
		}

		err := r.CreateTransaction(ctx, input)
		if err != nil {
			t.Fatalf("failed to create transaction %d: %v", i, err)
		}
	}

	// List first page
	transactions, nextCursor, err := r.ListTransactionsByFamily(ctx, "test-family-001", "", nil, 3)
	if err != nil {
		t.Fatalf("failed to list transactions: %v", err)
	}

	if len(transactions) != 3 {
		t.Errorf("expected 3 transactions in first page, got %d", len(transactions))
	}

	if nextCursor == nil {
		t.Error("expected next cursor for pagination")
	}

	// List second page
	transactions2, nextCursor2, err := r.ListTransactionsByFamily(ctx, "test-family-001", "", nextCursor, 3)
	if err != nil {
		t.Fatalf("failed to list second page: %v", err)
	}

	if len(transactions2) != 2 {
		t.Errorf("expected 2 transactions in second page, got %d", len(transactions2))
	}

	if nextCursor2 != nil {
		t.Error("expected no more pages")
	}
}

func TestSoftDeleteTransaction(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	transaction := &model.FinanceTransaction{
		FamilyID:    "test-family-001",
		Type:        "income",
		AmountCents: 10000,
		AccountID:   "test-account-001",
		OccurredAt:  time.Now(),
		Description: "Salary",
		Version:     1,
	}

	input := repo.CreateTransactionInput{
		Transaction: transaction,
		FamilyID:    "test-family-001",
		RequestData: transaction,
	}

	err := r.CreateTransaction(ctx, input)
	if err != nil {
		t.Fatalf("failed to create transaction: %v", err)
	}

	err = r.SoftDeleteTransaction(ctx, transaction.ID, "user-001")
	if err != nil {
		t.Fatalf("failed to soft delete transaction: %v", err)
	}

	// Verify it's not returned in normal queries
	transactions, _, err := r.ListTransactionsByFamily(ctx, "test-family-001", "", nil, 10)
	if err != nil {
		t.Fatalf("failed to list transactions: %v", err)
	}

	if len(transactions) != 0 {
		t.Errorf("expected 0 active transactions after soft delete, got %d", len(transactions))
	}
}

func TestCreateAndGetLedger(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	ledger := &model.FinanceLedger{
		FamilyID:    "test-family-001",
		Name:        "2024 Q1 Budget",
		MemberIDs:   []string{"member-001", "member-002"},
		PeriodStart: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC),
		SortOrder:   1,
		Version:     1,
	}

	err := r.CreateLedger(ctx, ledger)
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	retrieved, err := r.GetLedgerByID(ctx, ledger.ID)
	if err != nil {
		t.Fatalf("failed to get ledger: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected ledger to be retrieved")
	}

	if retrieved.Name != ledger.Name {
		t.Errorf("expected name %s, got %s", ledger.Name, retrieved.Name)
	}

	if len(retrieved.MemberIDs) != len(ledger.MemberIDs) {
		t.Errorf("expected %d members, got %d", len(ledger.MemberIDs), len(retrieved.MemberIDs))
	}
}

func TestListLedgersByFamily(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create multiple ledgers
	for i := 0; i < 3; i++ {
		ledger := &model.FinanceLedger{
			FamilyID:    "test-family-001",
			Name:        "Ledger " + string(rune('A'+i)),
			MemberIDs:   []string{"member-001"},
			PeriodStart: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			PeriodEnd:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			SortOrder:   int32(i + 1),
			Version:     1,
		}
		err := r.CreateLedger(ctx, ledger)
		if err != nil {
			t.Fatalf("failed to create ledger %d: %v", i, err)
		}
	}

	ledgers, err := r.ListLedgersByFamily(ctx, "test-family-001")
	if err != nil {
		t.Fatalf("failed to list ledgers: %v", err)
	}

	if len(ledgers) != 3 {
		t.Errorf("expected 3 ledgers, got %d", len(ledgers))
	}
}

func TestCreateAndGetBudget(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	budget := &model.FinanceBudget{
		FamilyID:    "test-family-001",
		CategoryID:  "test-category-001",
		AmountCents: 500000,
		Period:      "monthly",
		StartDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
		IsActive:    true,
		Version:     1,
	}

	err := r.CreateBudget(ctx, budget)
	if err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}

	retrieved, err := r.GetBudgetByID(ctx, budget.ID)
	if err != nil {
		t.Fatalf("failed to get budget: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected budget to be retrieved")
	}

	if retrieved.AmountCents != budget.AmountCents {
		t.Errorf("expected amount %d, got %d", budget.AmountCents, retrieved.AmountCents)
	}
}

func TestListBudgetsByFamilyWithPeriod(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create budgets with different periods
	periods := []string{"monthly", "quarterly", "yearly"}
	for i, period := range periods {
		budget := &model.FinanceBudget{
			FamilyID:    "test-family-001",
			CategoryID:  "test-category-001",
			AmountCents: int64((i + 1) * 100000),
			Period:      period,
			StartDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:     time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			IsActive:    true,
			Version:     1,
		}
		err := r.CreateBudget(ctx, budget)
		if err != nil {
			t.Fatalf("failed to create budget %d: %v", i, err)
		}
	}

	// List all budgets
	budgets, err := r.ListBudgetsByFamily(ctx, "test-family-001", nil)
	if err != nil {
		t.Fatalf("failed to list budgets: %v", err)
	}

	if len(budgets) != 3 {
		t.Errorf("expected 3 budgets, got %d", len(budgets))
	}

	// List only monthly budgets
	monthly := "monthly"
	budgetsMonthly, err := r.ListBudgetsByFamily(ctx, "test-family-001", &monthly)
	if err != nil {
		t.Fatalf("failed to list monthly budgets: %v", err)
	}

	if len(budgetsMonthly) != 1 {
		t.Errorf("expected 1 monthly budget, got %d", len(budgetsMonthly))
	}

	if budgetsMonthly[0].Period != "monthly" {
		t.Errorf("expected monthly period, got %s", budgetsMonthly[0].Period)
	}
}

func TestCheckExceeded(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create a category
	category := &model.FinanceCategory{
		FamilyID:  "test-family-001",
		Name:      "Food",
		IsActive:  true,
		SortOrder: 1,
		Version:   1,
	}
	err := r.CreateCategory(ctx, category)
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	// Create an account
	account := &model.FinanceAccount{
		FamilyID: "test-family-001",
		Name:     "Test Account",
		Type:     "bank",
		Balance:  1000000,
		Version:  1,
	}
	err = r.CreateAccount(ctx, account)
	if err != nil {
		t.Fatalf("failed to create account: %v", err)
	}

	// Create transactions that sum to 30000 cents
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		transaction := &model.FinanceTransaction{
			FamilyID:    "test-family-001",
			Type:        "expense",
			AmountCents: -10000,
			CategoryID:  &category.ID,
			AccountID:   account.ID,
			OccurredAt:  startDate.Add(time.Duration(i) * time.Hour),
			Description: "Expense " + string(rune('A'+i)),
			Version:     1,
		}
		input := repo.CreateTransactionInput{
			Transaction: transaction,
			FamilyID:    "test-family-001",
			RequestData: transaction,
		}
		err := r.CreateTransaction(ctx, input)
		if err != nil {
			t.Fatalf("failed to create transaction %d: %v", i, err)
		}
	}

	// Check if exceeded (budget would be 25000, spent is 30000)
	totalSpent, _, err := r.CheckExceeded(ctx, "test-family-001", category.ID, startDate, endDate)
	if err != nil {
		t.Fatalf("failed to check exceeded: %v", err)
	}

	if totalSpent != 30000 {
		t.Errorf("expected total spent 30000, got %d", totalSpent)
	}
}

func TestCreateAndGetBill(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	now := time.Now()
	dueAt := now.Add(7 * 24 * time.Hour)

	bill := &model.FinanceBill{
		FamilyID:    "test-family-001",
		PayeeID:     "test-payee-001",
		AmountCents: 150000,
		DueAt:       dueAt,
		Status:      "pending",
		Version:     1,
	}

	err := r.CreateBill(ctx, bill)
	if err != nil {
		t.Fatalf("failed to create bill: %v", err)
	}

	retrieved, err := r.GetBillByID(ctx, bill.ID)
	if err != nil {
		t.Fatalf("failed to get bill: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected bill to be retrieved")
	}

	if retrieved.AmountCents != bill.AmountCents {
		t.Errorf("expected amount %d, got %d", bill.AmountCents, retrieved.AmountCents)
	}

	if retrieved.Status != "pending" {
		t.Errorf("expected status pending, got %s", retrieved.Status)
	}
}

func TestListBillsByFamilyWithStatus(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	now := time.Now()

	// Create bills with different statuses
	statuses := []string{"pending", "paid", "overdue"}
	for i, status := range statuses {
		bill := &model.FinanceBill{
			FamilyID:    "test-family-001",
			PayeeID:     "test-payee-001",
			AmountCents: int64((i + 1) * 50000),
			DueAt:       now.Add(time.Duration(i+1) * 24 * time.Hour),
			Status:      status,
			Version:     1,
		}
		if status == "paid" {
			paidAt := now
			bill.PaidAt = &paidAt
		}
		err := r.CreateBill(ctx, bill)
		if err != nil {
			t.Fatalf("failed to create bill %d: %v", i, err)
		}
	}

	// List all bills
	bills, err := r.ListBillsByFamily(ctx, "test-family-001", nil)
	if err != nil {
		t.Fatalf("failed to list bills: %v", err)
	}

	if len(bills) != 3 {
		t.Errorf("expected 3 bills, got %d", len(bills))
	}

	// List only pending bills
	pending := "pending"
	billsPending, err := r.ListBillsByFamily(ctx, "test-family-001", &pending)
	if err != nil {
		t.Fatalf("failed to list pending bills: %v", err)
	}

	if len(billsPending) != 1 {
		t.Errorf("expected 1 pending bill, got %d", len(billsPending))
	}

	if billsPending[0].Status != "pending" {
		t.Errorf("expected pending status, got %s", billsPending[0].Status)
	}
}

func TestMarkBillAsPaid(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	now := time.Now()
	dueAt := now.Add(7 * 24 * time.Hour)

	bill := &model.FinanceBill{
		FamilyID:    "test-family-001",
		PayeeID:     "test-payee-001",
		AmountCents: 100000,
		DueAt:       dueAt,
		Status:      "pending",
		Version:     1,
	}

	err := r.CreateBill(ctx, bill)
	if err != nil {
		t.Fatalf("failed to create bill: %v", err)
	}

	// Mark as paid
	updatedBill, err := r.MarkAsPaid(ctx, bill.ID)
	if err != nil {
		t.Fatalf("failed to mark bill as paid: %v", err)
	}

	if updatedBill.Status != "paid" {
		t.Errorf("expected status paid, got %s", updatedBill.Status)
	}

	if updatedBill.PaidAt == nil {
		t.Error("expected PaidAt to be set")
	}

	if updatedBill.Version != 2 {
		t.Errorf("expected version 2, got %d", updatedBill.Version)
	}

	// Verify the bill is updated in database
	retrieved, err := r.GetBillByID(ctx, bill.ID)
	if err != nil {
		t.Fatalf("failed to get bill: %v", err)
	}

	if retrieved.Status != "paid" {
		t.Errorf("expected status paid in DB, got %s", retrieved.Status)
	}
}

func TestCreateAndGetLoan(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	loan := &model.FinanceLoan{
		FamilyID:       "test-family-001",
		LenderName:     "Alice",
		BorrowerName:   "Bob",
		PrincipalCents: 1000000, // 10,000 yuan
		InterestRate:   5.5,
		StartDate:      startDate,
		EndDate:        endDate,
		Status:         "active",
		Version:        1,
	}

	err := r.CreateLoan(ctx, loan)
	if err != nil {
		t.Fatalf("failed to create loan: %v", err)
	}

	retrieved, err := r.GetLoanByID(ctx, loan.ID)
	if err != nil {
		t.Fatalf("failed to get loan: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected loan to be retrieved")
	}

	if retrieved.PrincipalCents != loan.PrincipalCents {
		t.Errorf("expected principal %d, got %d", loan.PrincipalCents, retrieved.PrincipalCents)
	}

	if retrieved.Status != "active" {
		t.Errorf("expected status active, got %s", retrieved.Status)
	}
}

func TestListLoansByFamilyWithStatus(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create loans with different statuses
	statuses := []string{"active", "paid_off"}
	for i, status := range statuses {
		loan := &model.FinanceLoan{
			FamilyID:       "test-family-001",
			LenderName:     "Lender " + string(rune('A'+i)),
			BorrowerName:   "Borrower " + string(rune('A'+i)),
			PrincipalCents: int64((i + 1) * 500000),
			InterestRate:   float64(i+1) * 3.0,
			StartDate:      startDate,
			EndDate:        endDate,
			Status:         status,
			Version:        1,
		}
		err := r.CreateLoan(ctx, loan)
		if err != nil {
			t.Fatalf("failed to create loan %d: %v", i, err)
		}
	}

	// List all loans
	loans, err := r.ListLoansByFamily(ctx, "test-family-001", nil)
	if err != nil {
		t.Fatalf("failed to list loans: %v", err)
	}

	if len(loans) != 2 {
		t.Errorf("expected 2 loans, got %d", len(loans))
	}

	// List only active loans
	active := "active"
	loansActive, err := r.ListLoansByFamily(ctx, "test-family-001", &active)
	if err != nil {
		t.Fatalf("failed to list active loans: %v", err)
	}

	if len(loansActive) != 1 {
		t.Errorf("expected 1 active loan, got %d", len(loansActive))
	}

	if loansActive[0].Status != "active" {
		t.Errorf("expected active status, got %s", loansActive[0].Status)
	}
}

func TestPayOffLoan(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	loan := &model.FinanceLoan{
		FamilyID:       "test-family-001",
		LenderName:     "Alice",
		BorrowerName:   "Bob",
		PrincipalCents: 500000,
		InterestRate:   4.0,
		StartDate:      startDate,
		EndDate:        endDate,
		Status:         "active",
		Version:        1,
	}

	err := r.CreateLoan(ctx, loan)
	if err != nil {
		t.Fatalf("failed to create loan: %v", err)
	}

	// Pay off the loan
	updatedLoan, err := r.PayOffLoan(ctx, loan.ID)
	if err != nil {
		t.Fatalf("failed to pay off loan: %v", err)
	}

	if updatedLoan.Status != "paid_off" {
		t.Errorf("expected status paid_off, got %s", updatedLoan.Status)
	}

	if updatedLoan.Version != 2 {
		t.Errorf("expected version 2, got %d", updatedLoan.Version)
	}

	// Verify the loan is updated in database
	retrieved, err := r.GetLoanByID(ctx, loan.ID)
	if err != nil {
		t.Fatalf("failed to get loan: %v", err)
	}

	if retrieved.Status != "paid_off" {
		t.Errorf("expected status paid_off in DB, got %s", retrieved.Status)
	}
}

func TestCreateAndGetRepaymentPlan(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// First create a loan
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	loan := &model.FinanceLoan{
		FamilyID:       "test-family-001",
		LenderName:     "Alice",
		BorrowerName:   "Bob",
		PrincipalCents: 1000000,
		InterestRate:   5.0,
		StartDate:      startDate,
		EndDate:        endDate,
		Status:         "active",
		Version:        1,
	}

	err := r.CreateLoan(ctx, loan)
	if err != nil {
		t.Fatalf("failed to create loan: %v", err)
	}

	// Create repayment plan
	dueAt := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	plan := &model.FinanceRepaymentPlan{
		FamilyID:    "test-family-001",
		LoanID:      loan.ID,
		DueAt:       dueAt,
		AmountCents: 100000,
		Status:      "pending",
		Version:     1,
	}

	err = r.CreateRepaymentPlan(ctx, plan)
	if err != nil {
		t.Fatalf("failed to create repayment plan: %v", err)
	}

	retrieved, err := r.GetRepaymentPlanByID(ctx, plan.ID)
	if err != nil {
		t.Fatalf("failed to get repayment plan: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected repayment plan to be retrieved")
	}

	if retrieved.AmountCents != plan.AmountCents {
		t.Errorf("expected amount %d, got %d", plan.AmountCents, retrieved.AmountCents)
	}

	if retrieved.Status != "pending" {
		t.Errorf("expected status pending, got %s", retrieved.Status)
	}
}

func TestGetRepaymentPlansByLoanID(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create a loan
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	loan := &model.FinanceLoan{
		FamilyID:       "test-family-001",
		LenderName:     "Alice",
		BorrowerName:   "Bob",
		PrincipalCents: 1000000,
		InterestRate:   5.0,
		StartDate:      startDate,
		EndDate:        endDate,
		Status:         "active",
		Version:        1,
	}

	err := r.CreateLoan(ctx, loan)
	if err != nil {
		t.Fatalf("failed to create loan: %v", err)
	}

	// Create multiple repayment plans
	for i := 0; i < 3; i++ {
		plan := &model.FinanceRepaymentPlan{
			FamilyID:    "test-family-001",
			LoanID:      loan.ID,
			DueAt:       time.Date(2024, time.Month(2+i), 1, 0, 0, 0, 0, time.UTC),
			AmountCents: int64((i + 1) * 100000),
			Status:      "pending",
			Version:     1,
		}
		err := r.CreateRepaymentPlan(ctx, plan)
		if err != nil {
			t.Fatalf("failed to create repayment plan %d: %v", i, err)
		}
	}

	plans, err := r.GetRepaymentPlansByLoanID(ctx, loan.ID)
	if err != nil {
		t.Fatalf("failed to get repayment plans: %v", err)
	}

	if len(plans) != 3 {
		t.Errorf("expected 3 repayment plans, got %d", len(plans))
	}

	// Verify they are ordered by due_at
	for i := 0; i < len(plans)-1; i++ {
		if plans[i].DueAt.After(plans[i+1].DueAt) {
			t.Errorf("expected plans to be ordered by due_at, but plan %d is after plan %d", i, i+1)
		}
	}
}

func TestListOverdueRepaymentPlans(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create a loan
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	loan := &model.FinanceLoan{
		FamilyID:       "test-family-001",
		LenderName:     "Alice",
		BorrowerName:   "Bob",
		PrincipalCents: 1000000,
		InterestRate:   5.0,
		StartDate:      startDate,
		EndDate:        endDate,
		Status:         "active",
		Version:        1,
	}

	err := r.CreateLoan(ctx, loan)
	if err != nil {
		t.Fatalf("failed to create loan: %v", err)
	}

	now := time.Now()

	// Create an overdue plan (due date in the past)
	overduePlan := &model.FinanceRepaymentPlan{
		FamilyID:    "test-family-001",
		LoanID:      loan.ID,
		DueAt:       now.Add(-7 * 24 * time.Hour), // 7 days ago
		AmountCents: 100000,
		Status:      "pending",
		Version:     1,
	}
	err = r.CreateRepaymentPlan(ctx, overduePlan)
	if err != nil {
		t.Fatalf("failed to create overdue plan: %v", err)
	}

	// Create a future plan (not overdue)
	futurePlan := &model.FinanceRepaymentPlan{
		FamilyID:    "test-family-001",
		LoanID:      loan.ID,
		DueAt:       now.Add(7 * 24 * time.Hour), // 7 days from now
		AmountCents: 100000,
		Status:      "pending",
		Version:     1,
	}
	err = r.CreateRepaymentPlan(ctx, futurePlan)
	if err != nil {
		t.Fatalf("failed to create future plan: %v", err)
	}

	// List overdue plans
	plans, err := r.ListOverdueRepaymentPlans(ctx)
	if err != nil {
		t.Fatalf("failed to list overdue plans: %v", err)
	}

	if len(plans) != 1 {
		t.Errorf("expected 1 overdue plan, got %d", len(plans))
	}

	if len(plans) > 0 && plans[0].ID != overduePlan.ID {
		t.Errorf("expected overdue plan ID %s, got %s", overduePlan.ID, plans[0].ID)
	}
}

func TestMarkRepaymentPlanAsPaid(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create a loan
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	loan := &model.FinanceLoan{
		FamilyID:       "test-family-001",
		LenderName:     "Alice",
		BorrowerName:   "Bob",
		PrincipalCents: 1000000,
		InterestRate:   5.0,
		StartDate:      startDate,
		EndDate:        endDate,
		Status:         "active",
		Version:        1,
	}

	err := r.CreateLoan(ctx, loan)
	if err != nil {
		t.Fatalf("failed to create loan: %v", err)
	}

	// Create a repayment plan
	dueAt := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	plan := &model.FinanceRepaymentPlan{
		FamilyID:    "test-family-001",
		LoanID:      loan.ID,
		DueAt:       dueAt,
		AmountCents: 100000,
		Status:      "pending",
		Version:     1,
	}

	err = r.CreateRepaymentPlan(ctx, plan)
	if err != nil {
		t.Fatalf("failed to create repayment plan: %v", err)
	}

	// Mark as paid
	updatedPlan, err := r.MarkRepaymentPlanAsPaid(ctx, plan.ID)
	if err != nil {
		t.Fatalf("failed to mark repayment plan as paid: %v", err)
	}

	if updatedPlan.Status != "paid" {
		t.Errorf("expected status paid, got %s", updatedPlan.Status)
	}

	if updatedPlan.PaidAt == nil {
		t.Error("expected PaidAt to be set")
	}

	if updatedPlan.Version != 2 {
		t.Errorf("expected version 2, got %d", updatedPlan.Version)
	}

	// Verify the plan is updated in database
	retrieved, err := r.GetRepaymentPlanByID(ctx, plan.ID)
	if err != nil {
		t.Fatalf("failed to get repayment plan: %v", err)
	}

	if retrieved.Status != "paid" {
		t.Errorf("expected status paid in DB, got %s", retrieved.Status)
	}
}

func TestCreateAndGetGoal(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	deadline := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)

	goal := &model.FinanceGoal{
		FamilyID:          "test-family-001",
		Name:              "Vacation Fund",
		TargetAmountCents: 5000000, // 50,000 yuan
		CurrentAmountCents: 0,
		Deadline:          deadline,
		IsAchieved:        false,
		Version:           1,
	}

	err := r.CreateGoal(ctx, goal)
	if err != nil {
		t.Fatalf("failed to create goal: %v", err)
	}

	retrieved, err := r.GetGoalByID(ctx, goal.ID)
	if err != nil {
		t.Fatalf("failed to get goal: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected goal to be retrieved")
	}

	if retrieved.TargetAmountCents != goal.TargetAmountCents {
		t.Errorf("expected target amount %d, got %d", goal.TargetAmountCents, retrieved.TargetAmountCents)
	}

	if retrieved.CurrentAmountCents != 0 {
		t.Errorf("expected current amount 0, got %d", retrieved.CurrentAmountCents)
	}

	if retrieved.IsAchieved {
		t.Error("expected IsAchieved to be false")
	}
}

func TestListGoalsByFamily(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	// Create multiple goals
	for i := 0; i < 3; i++ {
		goal := &model.FinanceGoal{
			FamilyID:          "test-family-001",
			Name:              "Goal " + string(rune('A'+i)),
			TargetAmountCents: int64((i + 1) * 1000000),
			CurrentAmountCents: 0,
			Deadline:          time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			IsAchieved:        false,
			Version:           1,
		}
		err := r.CreateGoal(ctx, goal)
		if err != nil {
			t.Fatalf("failed to create goal %d: %v", i, err)
		}
	}

	goals, err := r.ListGoalsByFamily(ctx, "test-family-001")
	if err != nil {
		t.Fatalf("failed to list goals: %v", err)
	}

	if len(goals) != 3 {
		t.Errorf("expected 3 goals, got %d", len(goals))
	}
}

func TestUpdateGoalProgress(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	deadline := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)

	goal := &model.FinanceGoal{
		FamilyID:          "test-family-001",
		Name:              "Savings Goal",
		TargetAmountCents: 1000000, // 10,000 yuan
		CurrentAmountCents: 0,
		Deadline:          deadline,
		IsAchieved:        false,
		Version:           1,
	}

	err := r.CreateGoal(ctx, goal)
	if err != nil {
		t.Fatalf("failed to create goal: %v", err)
	}

	// Update progress to 50%
	updatedGoal, err := r.UpdateGoalProgress(ctx, goal.ID, 500000)
	if err != nil {
		t.Fatalf("failed to update goal progress: %v", err)
	}

	if updatedGoal.CurrentAmountCents != 500000 {
		t.Errorf("expected current amount 500000, got %d", updatedGoal.CurrentAmountCents)
	}

	if updatedGoal.IsAchieved {
		t.Error("expected IsAchieved to still be false at 50%")
	}

	if updatedGoal.Version != 2 {
		t.Errorf("expected version 2, got %d", updatedGoal.Version)
	}

	// Update progress to 100% (achieved)
	updatedGoal2, err := r.UpdateGoalProgress(ctx, goal.ID, 1000000)
	if err != nil {
		t.Fatalf("failed to update goal progress to 100%%: %v", err)
	}

	if !updatedGoal2.IsAchieved {
		t.Error("expected IsAchieved to be true at 100%")
	}

	// Verify the goal is updated in database
	retrieved, err := r.GetGoalByID(ctx, goal.ID)
	if err != nil {
		t.Fatalf("failed to get goal: %v", err)
	}

	if retrieved.CurrentAmountCents != 1000000 {
		t.Errorf("expected current amount 1000000 in DB, got %d", retrieved.CurrentAmountCents)
	}

	if !retrieved.IsAchieved {
		t.Error("expected IsAchieved to be true in DB")
	}
}
