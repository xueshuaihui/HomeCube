// Package ocr provides OCR (Optical Character Recognition) adapter interfaces and implementations.
package ocr

import (
	"context"
	"time"
)

// ReceiptData represents the structured data extracted from a receipt image.
type ReceiptData struct {
	Amount   float64 `json:"amount"`    // Amount in yuan
	Category string  `json:"category"`  // Expense category
	Date     string  `json:"date"`      // Transaction date (YYYY-MM-DD format)
	Merchant string  `json:"merchant"`  // Merchant name
}

// OCRAdapter defines the interface for OCR receipt recognition.
type OCRAdapter interface {
	// RecognizeReceipt recognizes receipt information from an image.
	// Parameters:
	//   - ctx: Context for cancellation and timeouts
	//   - imageBytes: Raw image bytes (JPEG, PNG, etc.)
	//
	// Returns structured receipt data or an error.
	RecognizeReceipt(ctx context.Context, imageBytes []byte) (ReceiptData, error)
}

// StubAdapter is a stub implementation of OCRAdapter for development/testing.
// This returns fixed mock data until a real OCR provider is selected.
type StubAdapter struct{}

// NewStubAdapter creates a new stub OCR adapter.
func NewStubAdapter() *StubAdapter {
	return &StubAdapter{}
}

// RecognizeReceipt implements the OCRAdapter interface with mock data.
// This stub always returns the same fixed receipt data for testing purposes.
func (s *StubAdapter) RecognizeReceipt(ctx context.Context, imageBytes []byte) (ReceiptData, error) {
	// Return fixed mock data as specified in the requirements
	return ReceiptData{
		Amount:   50.00,
		Category: "餐饮",
		Date:     time.Now().Format("2006-01-02"),
		Merchant: "测试餐厅",
	}, nil
}
