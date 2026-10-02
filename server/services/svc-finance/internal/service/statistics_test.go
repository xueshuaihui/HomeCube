package service

import (
	"testing"
)

func TestValidatePeriod(t *testing.T) {
	tests := []struct {
		name    string
		period  string
		wantErr bool
	}{
		{"empty period is valid", "", false},
		{"valid month format", "2026-09", false},
		{"valid quarter format Q1", "2026-Q1", false},
		{"valid quarter format Q2", "2026-Q2", false},
		{"valid quarter format Q3", "2026-Q3", false},
		{"valid quarter format Q4", "2026-Q4", false},
		{"valid year format", "2026", false},
		{"invalid week format", "2026-W1", true},
		{"invalid month number", "2026-13", true},
		{"invalid month zero", "2026-00", true},
		{"invalid quarter Q5", "2026-Q5", true},
		{"invalid quarter Q0", "2026-Q0", true},
		{"invalid format slash", "2026/09", true},
		{"invalid format timestamp", "1234567890", true},
		{"invalid year too small", "1899", true},
		{"invalid year too large", "2101", true},
		{"valid boundary year 1900", "1900", false},
		{"valid boundary year 2100", "2100", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePeriod(tt.period)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePeriod(%q) error = %v, wantErr %v", tt.period, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePeriodInvalidFormats(t *testing.T) {
	// Test specific invalid formats that should return 400 and not silently fallback
	invalidPeriods := []string{
		"2026-W1",  // Week format - explicitly mentioned in requirements
		"2026-W52", // Another week format
		"2026M09",  // Wrong separator
		"Sep-2026", // Wrong order
		"2026-9",   // Single digit month
		"2026-Q",   // Missing quarter number
		"2026-Q10", // Invalid quarter number
		"",         // Empty is actually valid for lifetime
	}

	for _, period := range invalidPeriods {
		if period == "" {
			continue // Empty is valid
		}
		err := ValidatePeriod(period)
		if err == nil {
			t.Errorf("ValidatePeriod(%q) should return error for invalid format", period)
		}
	}
}
