package ocr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStubAdapter_RecognizeReceipt(t *testing.T) {
	adapter := NewStubAdapter()
	ctx := context.Background()

	// Test with empty image bytes
	data, err := adapter.RecognizeReceipt(ctx, []byte{})
	assert.NoError(t, err)
	assert.NotNil(t, data)

	// Verify mock data structure
	assert.Equal(t, 50.00, data.Amount)
	assert.Equal(t, "餐饮", data.Category)
	assert.NotEmpty(t, data.Date)
	assert.Equal(t, "测试餐厅", data.Merchant)

	// Test with non-empty image bytes (should still return same mock data)
	fakeImage := []byte("fake image data")
	data2, err := adapter.RecognizeReceipt(ctx, fakeImage)
	assert.NoError(t, err)
	assert.Equal(t, data.Amount, data2.Amount)
	assert.Equal(t, data.Category, data2.Category)
}

func TestReceiptData_Structure(t *testing.T) {
	data := ReceiptData{
		Amount:   100.50,
		Category: "交通",
		Date:     "2024-01-15",
		Merchant: "出租车",
	}

	assert.Equal(t, 100.50, data.Amount)
	assert.Equal(t, "交通", data.Category)
	assert.Equal(t, "2024-01-15", data.Date)
	assert.Equal(t, "出租车", data.Merchant)
}
