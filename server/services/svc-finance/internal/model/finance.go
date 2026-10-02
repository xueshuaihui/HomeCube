// Package model defines GORM models for the finance service.
package model

import (
	"time"

	"gorm.io/gorm"
)

// TablePrefix is the schema prefix for finance tables.
// In production with PostgreSQL, this should be "finance".
// For testing with SQLite, this can be empty.
var TablePrefix = ""

// setTableName returns the full table name with optional schema prefix.
func setTableName(baseName string) string {
	if TablePrefix != "" {
		return TablePrefix + "." + baseName
	}
	return baseName
}

// FinanceAccount represents a financial account (finance_account table).
type FinanceAccount struct {
	ID         string         `gorm:"primaryKey" json:"id"`
	FamilyID   string         `gorm:"not null;index:idx_finance_account_family_id" json:"family_id"`
	Name       string         `gorm:"type:text;not null" json:"name"`
	Type       string         `gorm:"type:text;not null" json:"type"`                // e.g., "cash", "bank", "credit_card"
	Balance    int64          `gorm:"type:bigint;not null;default:0" json:"balance"` // amount in cents
	IsArchived bool           `gorm:"type:boolean;not null;default:false" json:"is_archived"`
	ArchivedAt *time.Time     `json:"archived_at,omitempty"`
	Version    int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for FinanceAccount.
func (FinanceAccount) TableName() string {
	return setTableName("finance_account")
}

// FinanceCategory represents a financial category (finance_category table).
type FinanceCategory struct {
	ID        string         `gorm:"primaryKey" json:"id"`
	FamilyID  string         `gorm:"not null;index:idx_finance_category_family_id" json:"family_id"`
	Name      string         `gorm:"type:text;not null" json:"name"`
	Icon      string         `gorm:"type:text" json:"icon,omitempty"`
	SortOrder int32          `gorm:"type:integer;not null;default:0" json:"sort_order"`
	IsActive  bool           `gorm:"type:boolean;not null;default:true" json:"is_active"`
	Version   int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for FinanceCategory.
func (FinanceCategory) TableName() string {
	return setTableName("finance_category")
}

// FinanceTransaction represents a financial transaction (finance_transaction table).
type FinanceTransaction struct {
	ID              string         `gorm:"primaryKey" json:"id"`
	FamilyID        string         `gorm:"not null;index:idx_finance_transaction_family_id" json:"family_id"`
	Type            string         `gorm:"type:text;not null" json:"type"`           // "income", "expense", "transfer"
	AmountCents     int64          `gorm:"type:bigint;not null" json:"amount_cents"` // amount in cents, positive for income, negative for expense
	CategoryID      *string        `gorm:"index:idx_finance_transaction_category_id" json:"category_id,omitempty"`
	AccountID       string         `gorm:"not null;index:idx_finance_transaction_account_id" json:"account_id"`
	OccurredAt      time.Time      `gorm:"not null" json:"occurred_at"`
	Description     string         `gorm:"type:text" json:"description,omitempty"`
	ReceiptFileID   *string        `json:"receipt_file_id,omitempty"`
	TransferGroupID *string        `gorm:"index:idx_finance_transaction_transfer_group_id" json:"transfer_group_id,omitempty"`
	ClientRequestID *string        `gorm:"uniqueIndex:uk_client_request_id" json:"client_request_id,omitempty"`
	Version         int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
	DeletedBy       *string        `json:"deleted_by,omitempty"`
}

// TableName returns the table name for FinanceTransaction.
func (FinanceTransaction) TableName() string {
	return setTableName("finance_transaction")
}

// FinanceLedger represents a financial ledger/book (finance_ledger table).
type FinanceLedger struct {
	ID          string         `gorm:"primaryKey" json:"id"`
	FamilyID    string         `gorm:"not null;index:idx_finance_ledger_family_id" json:"family_id"`
	Name        string         `gorm:"type:text;not null" json:"name"`
	MemberIDs   []string       `gorm:"serializer:json;not null" json:"member_ids"` // array of member UUIDs
	PeriodStart time.Time      `gorm:"type:date;not null" json:"period_start"`
	PeriodEnd   time.Time      `gorm:"type:date;not null" json:"period_end"`
	SortOrder   int32          `gorm:"type:integer;not null;default:0" json:"sort_order"`
	Version     int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for FinanceLedger.
func (FinanceLedger) TableName() string {
	return setTableName("finance_ledger")
}

// FinanceBudget represents a financial budget (finance_budget table).
type FinanceBudget struct {
	ID          string         `gorm:"primaryKey" json:"id"`
	FamilyID    string         `gorm:"not null;index:idx_finance_budget_family_id" json:"family_id"`
	CategoryID  string         `gorm:"not null;index:idx_finance_budget_category_id" json:"category_id"`
	AmountCents int64          `gorm:"type:bigint;not null" json:"amount_cents"` // amount in cents
	Period      string         `gorm:"type:text;not null" json:"period"`         // "monthly", "quarterly", "yearly"
	StartDate   time.Time      `gorm:"type:date;not null" json:"start_date"`
	EndDate     time.Time      `gorm:"type:date;not null" json:"end_date"`
	IsActive    bool           `gorm:"type:boolean;not null;default:true" json:"is_active"`
	Version     int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for FinanceBudget.
func (FinanceBudget) TableName() string {
	return setTableName("finance_budget")
}

// FinanceBill represents a financial bill (finance_bill table).
type FinanceBill struct {
	ID          string         `gorm:"primaryKey" json:"id"`
	FamilyID    string         `gorm:"not null;index:idx_finance_bill_family_id" json:"family_id"`
	PayeeID     string         `gorm:"not null;index:idx_finance_bill_payee_id" json:"payee_id"`
	AmountCents int64          `gorm:"type:bigint;not null" json:"amount_cents"` // amount in cents
	DueAt       time.Time      `gorm:"not null" json:"due_at"`
	PaidAt      *time.Time     `json:"paid_at,omitempty"`
	Status      string         `gorm:"type:text;not null;default:'pending'" json:"status"` // "pending", "paid", "overdue"
	Version     int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for FinanceBill.
func (FinanceBill) TableName() string {
	return setTableName("finance_bill")
}

// FinanceLoan represents a loan between parties (finance_loan table).
type FinanceLoan struct {
	ID             string         `gorm:"primaryKey" json:"id"`
	FamilyID       string         `gorm:"not null;index:idx_finance_loan_family_id" json:"family_id"`
	LenderName     string         `gorm:"type:text;not null" json:"lender_name"`
	BorrowerName   string         `gorm:"type:text;not null" json:"borrower_name"`
	PrincipalCents int64          `gorm:"type:bigint;not null" json:"principal_cents"`               // amount in cents
	InterestRate   float64        `gorm:"type:numeric(5,2);not null;default:0" json:"interest_rate"` // annual interest rate as percentage (e.g., 5.5 for 5.5%)
	StartDate      time.Time      `gorm:"type:date;not null" json:"start_date"`
	EndDate        time.Time      `gorm:"type:date;not null" json:"end_date"`
	Status         string         `gorm:"type:text;not null;default:'active'" json:"status"` // "active", "paid_off"
	Version        int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for FinanceLoan.
func (FinanceLoan) TableName() string {
	return setTableName("finance_loan")
}

// FinanceRepaymentPlan represents a repayment installment for a loan (finance_repayment_plan table).
type FinanceRepaymentPlan struct {
	ID          string         `gorm:"primaryKey" json:"id"`
	FamilyID    string         `gorm:"not null;index:idx_finance_repayment_plan_family_id" json:"family_id"`
	LoanID      string         `gorm:"not null;index:idx_finance_repayment_plan_loan_id" json:"loan_id"`
	DueAt       time.Time      `gorm:"not null;index:idx_finance_repayment_plan_due_at" json:"due_at"`
	AmountCents int64          `gorm:"type:bigint;not null" json:"amount_cents"` // amount in cents
	PaidAt      *time.Time     `json:"paid_at,omitempty"`
	Status      string         `gorm:"type:text;not null;default:'pending'" json:"status"` // "pending", "paid", "overdue"
	Version     int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for FinanceRepaymentPlan.
func (FinanceRepaymentPlan) TableName() string {
	return setTableName("finance_repayment_plan")
}

// FinanceGoal represents a savings goal (finance_goal table).
type FinanceGoal struct {
	ID                 string         `gorm:"primaryKey" json:"id"`
	FamilyID           string         `gorm:"not null;index:idx_finance_goal_family_id" json:"family_id"`
	Name               string         `gorm:"type:text;not null" json:"name"`
	TargetAmountCents  int64          `gorm:"type:bigint;not null" json:"target_amount_cents"`            // amount in cents
	CurrentAmountCents int64          `gorm:"type:bigint;not null;default:0" json:"current_amount_cents"` // amount in cents
	Deadline           time.Time      `gorm:"type:date;not null" json:"deadline"`
	IsAchieved         bool           `gorm:"type:boolean;not null;default:false" json:"is_achieved"`
	Version            int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for FinanceGoal.
func (FinanceGoal) TableName() string {
	return setTableName("finance_goal")
}

// FinanceSplitSettlement represents a split settlement record (finance_split_settlement table).
type FinanceSplitSettlement struct {
	ID               string         `gorm:"primaryKey" json:"id"`
	FamilyID         string         `gorm:"not null;index:idx_finance_split_settlement_family_id" json:"family_id"`
	TransactionID    string         `gorm:"not null;index:idx_finance_split_settlement_transaction_id" json:"transaction_id"`
	Status           string         `gorm:"type:text;not null;default:'draft'" json:"status"` // "draft", "pending", "settled"
	TotalAmountCents int64          `gorm:"type:bigint;not null" json:"total_amount_cents"`   // total split amount in cents
	SettledAt        *time.Time     `json:"settled_at,omitempty"`
	Version          int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
	DeletedBy        *string        `json:"deleted_by,omitempty"`
}

// TableName returns the table name for FinanceSplitSettlement.
func (FinanceSplitSettlement) TableName() string {
	return setTableName("finance_split_settlement")
}

// FinanceParticipant represents a participant in a split settlement (finance_participant table).
type FinanceParticipant struct {
	ID               string         `gorm:"primaryKey" json:"id"`
	SettlementID     string         `gorm:"not null;index:idx_finance_participant_settlement_id" json:"settlement_id"`
	AccountID        string         `gorm:"not null;index:idx_finance_participant_account_id" json:"account_id"`
	ShareRatio       float64        `gorm:"type:numeric(5,4);not null" json:"share_ratio"`      // share ratio (0.0000 - 1.0000)
	ShareAmountCents int64          `gorm:"type:bigint;not null" json:"share_amount_cents"`     // share amount in cents
	Status           string         `gorm:"type:text;not null;default:'pending'" json:"status"` // "pending", "paid"
	PaidAt           *time.Time     `json:"paid_at,omitempty"`
	Version          int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
	DeletedBy        *string        `json:"deleted_by,omitempty"`
}

// TableName returns the table name for FinanceParticipant.
func (FinanceParticipant) TableName() string {
	return setTableName("finance_participant")
}

// FinanceCreditCard represents a credit card account (finance_credit_card table).
type FinanceCreditCard struct {
	ID                  string         `gorm:"primaryKey" json:"id"`
	FamilyID            string         `gorm:"not null;index:idx_finance_credit_card_family_id" json:"family_id"`
	CardNumberHash      string         `gorm:"type:text;not null;uniqueIndex:uk_finance_credit_card_card_number_hash" json:"card_number_hash"` // hashed card number
	Issuer              string         `gorm:"type:text;not null" json:"issuer"`                                                               // card issuer
	BillingDay          int32          `gorm:"type:integer;not null" json:"billing_day"`                                                       // billing day (1-31)
	DueDay              int32          `gorm:"type:integer;not null" json:"due_day"`                                                           // due day (1-31)
	CreditLimitCents    int64          `gorm:"type:bigint;not null" json:"credit_limit_cents"`                                                 // credit limit in cents
	CurrentBalanceCents int64          `gorm:"type:bigint;not null;default:0" json:"current_balance_cents"`                                    // current balance in cents (positive means owed)
	Currency            string         `gorm:"type:text;not null;default:'CNY'" json:"currency"`                                               // currency code
	Status              string         `gorm:"type:text;not null;default:'active'" json:"status"`                                              // "active", "frozen", "closed"
	Version             int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
	DeletedBy           *string        `json:"deleted_by,omitempty"`
}

// TableName returns the table name for FinanceCreditCard.
func (FinanceCreditCard) TableName() string {
	return setTableName("finance_credit_card")
}

// FinanceInvoice represents an invoice record (finance_invoice table).
type FinanceInvoice struct {
	ID                  string         `gorm:"primaryKey" json:"id"`
	FamilyID            string         `gorm:"not null;index:idx_finance_invoice_family_id" json:"family_id"`
	InvoiceNumber       string         `gorm:"type:text;not null;uniqueIndex:uk_finance_invoice_number" json:"invoice_number"` // invoice number
	AmountCents         int64          `gorm:"type:bigint;not null" json:"amount_cents"`                                       // invoice amount in cents
	TaxAmountCents      int64          `gorm:"type:bigint;not null;default:0" json:"tax_amount_cents"`                         // tax amount in cents
	Vendor              string         `gorm:"type:text;not null" json:"vendor"`                                               // vendor/supplier
	IssueDate           time.Time      `gorm:"type:date;not null" json:"issue_date"`                                           // issue date
	ReimbursementStatus string         `gorm:"type:text;not null;default:'pending'" json:"reimbursement_status"`               // "pending", "reimbursed", "rejected"
	ReimbursedAt        *time.Time     `json:"reimbursed_at,omitempty"`
	RejectedReason      *string        `json:"rejected_reason,omitempty"`
	TransactionID       *string        `gorm:"index:idx_finance_invoice_transaction_id" json:"transaction_id,omitempty"` // related transaction ID
	Version             int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
	DeletedBy           *string        `json:"deleted_by,omitempty"`
}

// TableName returns the table name for FinanceInvoice.
func (FinanceInvoice) TableName() string {
	return setTableName("finance_invoice")
}

// FinanceAssetLiabilityReport represents an asset-liability report snapshot (finance_asset_liability_report table).
type FinanceAssetLiabilityReport struct {
	ID                    string         `gorm:"primaryKey" json:"id"`
	FamilyID              string         `gorm:"not null;index:idx_finance_asset_liability_report_family_id" json:"family_id"`
	Period                string         `gorm:"type:text;not null;uniqueIndex:uk_finance_asset_liability_report_family_period" json:"period"` // period format: YYYY-MM
	TotalAssetsCents      int64          `gorm:"type:bigint;not null;default:0" json:"total_assets_cents"`                                     // total assets in cents
	TotalLiabilitiesCents int64          `gorm:"type:bigint;not null;default:0" json:"total_liabilities_cents"`                                // total liabilities in cents
	NetWorthCents         int64          `gorm:"type:bigint;not null" json:"net_worth_cents"`                                                  // net worth = assets - liabilities
	SnapshotAt            time.Time      `gorm:"not null" json:"snapshot_at"`                                                                  // snapshot generation time
	Details               string         `gorm:"type:jsonb" json:"details,omitempty"`                                                          // detailed breakdown (optional)
	Version               int64          `gorm:"type:bigint;not null;default:1" json:"version"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
	DeletedAt             gorm.DeletedAt `gorm:"index" json:"-"`
	DeletedBy             *string        `json:"deleted_by,omitempty"`
}

// TableName returns the table name for FinanceAssetLiabilityReport.
func (FinanceAssetLiabilityReport) TableName() string {
	return setTableName("finance_asset_liability_report")
}
