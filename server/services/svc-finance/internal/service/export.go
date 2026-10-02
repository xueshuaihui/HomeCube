// Package service provides business logic for the finance service.
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"gorm.io/gorm"
)

// ExportService provides data export operations.
type ExportService struct {
	db *gorm.DB
}

// NewExportService creates a new export service instance.
func NewExportService(db *gorm.DB) *ExportService {
	return &ExportService{
		db: db,
	}
}

// ExportTransactions exports transactions based on type filter, days range, and format.
// Parameters:
//   - familyID: Family ID to filter transactions
//   - typeFilter: Transaction type filter (income/expense/transfer/all)
//   - days: Number of recent days to export (1-365)
//   - format: Export format (csv/xlsx)
//
// Returns byte stream and filename.
func (s *ExportService) ExportTransactions(ctx context.Context, familyID string, typeFilter string, days int, format string) ([]byte, string, error) {
	// Validate parameters
	if err := validateExportParams(typeFilter, days, format); err != nil {
		return nil, "", err
	}

	// Calculate date range
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)

	// Query transactions
	var transactions []model.FinanceTransaction
	query := s.db.WithContext(ctx).
		Model(&model.FinanceTransaction{}).
		Where("family_id = ? AND deleted_at IS NULL AND occurred_at >= ? AND occurred_at <= ?",
			familyID, startDate, endDate)

	// Apply type filter
	if typeFilter != "all" {
		query = query.Where("type = ?", typeFilter)
	}

	query = query.Order("occurred_at DESC")

	if err := query.Find(&transactions).Error; err != nil {
		return nil, "", fmt.Errorf("failed to query transactions: %w", err)
	}

	// Generate export based on format
	switch format {
	case "csv":
		data, filename, err := s.exportToCSV(transactions, typeFilter, days)
		if err != nil {
			return nil, "", fmt.Errorf("failed to export CSV: %w", err)
		}
		return data, filename, nil
	case "xlsx":
		data, filename, err := s.exportToXLSX(transactions, typeFilter, days)
		if err != nil {
			return nil, "", fmt.Errorf("failed to export XLSX: %w", err)
		}
		return data, filename, nil
	default:
		return nil, "", fmt.Errorf("unsupported format: %s", format)
	}
}

// validateExportParams validates export parameters.
func validateExportParams(typeFilter string, days int, format string) error {
	// Validate type filter
	validTypes := map[string]bool{
		"income":   true,
		"expense":  true,
		"transfer": true,
		"all":      true,
	}
	if !validTypes[typeFilter] {
		return fmt.Errorf("invalid type filter: %s (must be income, expense, transfer, or all)", typeFilter)
	}

	// Validate days
	if days < 1 || days > 365 {
		return fmt.Errorf("invalid days: %d (must be between 1 and 365)", days)
	}

	// Validate format
	validFormats := map[string]bool{
		"csv":  true,
		"xlsx": true,
	}
	if !validFormats[format] {
		return fmt.Errorf("invalid format: %s (must be csv or xlsx)", format)
	}

	return nil
}

// exportToCSV exports transactions to CSV format.
func (s *ExportService) exportToCSV(transactions []model.FinanceTransaction, typeFilter string, days int) ([]byte, string, error) {
	// Build CSV content
	var sb strings.Builder

	// Write header
	sb.WriteString("ID,Type,Amount,Currency,Category,Account,Occurred At,Description\n")

	// Write data rows
	for _, t := range transactions {
		categoryID := ""
		if t.CategoryID != nil {
			categoryID = *t.CategoryID
		}

		// Format amount (convert from cents to yuan with 2 decimal places)
		amount := float64(t.AmountCents) / 100.0

		// Escape description for CSV
		description := strings.ReplaceAll(t.Description, "\"", "\"\"")
		if strings.Contains(description, ",") || strings.Contains(description, "\"") || strings.Contains(description, "\n") {
			description = fmt.Sprintf("\"%s\"", description)
		}

		sb.WriteString(fmt.Sprintf("%s,%s,%.2f,CNY,%s,%s,%s,%s\n",
			t.ID,
			t.Type,
			amount,
			categoryID,
			t.AccountID,
			t.OccurredAt.Format("2006-01-02 15:04:05"),
			description,
		))
	}

	filename := fmt.Sprintf("transactions_%s_%ddays.csv", typeFilter, days)
	return []byte(sb.String()), filename, nil
}

// exportToXLSX exports transactions to XLSX format.
func (s *ExportService) exportToXLSX(transactions []model.FinanceTransaction, typeFilter string, days int) ([]byte, string, error) {
	// Create new Excel file
	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			// Log error but don't fail the export
			_ = err
		}
	}()

	// Set sheet name
	sheetName := "Transactions"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create sheet: %w", err)
	}

	// Set active sheet
	f.SetActiveSheet(index)

	// Define headers
	headers := []string{"ID", "Type", "Amount", "Currency", "Category", "Account", "Occurred At", "Description"}

	// Write headers
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheetName, cell, header)
	}

	// Style for headers
	style, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold: true,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#E0E0E0"},
			Pattern: 1,
		},
	})
	f.SetCellStyle(sheetName, "A1", "H1", style)

	// Write data rows
	for rowIdx, t := range transactions {
		rowNum := rowIdx + 2 // Start from row 2 (row 1 is header)

		categoryID := ""
		if t.CategoryID != nil {
			categoryID = *t.CategoryID
		}

		// Format amount (convert from cents to yuan with 2 decimal places)
		amount := float64(t.AmountCents) / 100.0

		// Set cell values
		f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), t.ID)
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), t.Type)
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", rowNum), amount)
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", rowNum), "CNY")
		f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), categoryID)
		f.SetCellValue(sheetName, fmt.Sprintf("F%d", rowNum), t.AccountID)
		f.SetCellValue(sheetName, fmt.Sprintf("G%d", rowNum), t.OccurredAt.Format("2006-01-02 15:04:05"))
		f.SetCellValue(sheetName, fmt.Sprintf("H%d", rowNum), t.Description)
	}

	// Auto-adjust column widths
	for i := 1; i <= len(headers); i++ {
		colName, _ := excelize.ColumnNumberToName(i)
		f.SetColWidth(sheetName, colName, colName, 20)
	}

	// Delete default Sheet1
	f.DeleteSheet("Sheet1")

	// Write to buffer
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, "", fmt.Errorf("failed to write Excel file: %w", err)
	}

	filename := fmt.Sprintf("transactions_%s_%ddays.xlsx", typeFilter, days)
	return buf.Bytes(), filename, nil
}
