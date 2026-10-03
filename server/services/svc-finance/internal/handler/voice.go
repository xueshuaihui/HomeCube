// Package handler provides HTTP handlers for the finance service.
package handler

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xueshuaihui/HomeCube/server/packages/adapter/asr"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
)

// VoiceHandler holds dependencies for voice-related handlers.
type VoiceHandler struct {
	repo       *repo.FinanceRepo
	asrAdapter asr.ASRAdapter
}

// NewVoiceHandler creates a new voice handler instance.
func NewVoiceHandler(repo *repo.FinanceRepo, asrAdapter asr.ASRAdapter) *VoiceHandler {
	return &VoiceHandler{
		repo:       repo,
		asrAdapter: asrAdapter,
	}
}

// VoiceEntryRequest represents the request body for voice entry.
//
// Two binding shapes, one type: the same endpoint takes a JSON body (no audio part, the manual
// draft path) and a multipart/form-data body (audio part + text fields, the ASR path). gin picks
// the mapper from Content-Type, and the form mapper reads the `form` tag -- falling back to the Go
// field name when there is none (gin@v1.12.0/binding/form_mapping.go:149-154). Without the `form`
// tags below a real multipart client, which sends the documented family_id / account_id, would bind
// nothing, fail `required,uuid` and get 400 before c.FormFile("audio") is ever reached: the whole
// ASR branch would be unreachable over HTTP. The json and form names are kept identical so both
// shapes carry the same field names.
type VoiceEntryRequest struct {
	FamilyID    string `json:"family_id" form:"family_id" binding:"required,uuid"`
	AccountID   string `json:"account_id" form:"account_id" binding:"required,uuid"`
	Description string `json:"description,omitempty" form:"description"` // Optional manual description override
}

// VoiceEntryResponse represents the response for voice entry.
type VoiceEntryResponse struct {
	Draft      *model.FinanceTransaction `json:"draft"`
	Transcript string                    `json:"transcript"`
	Source     string                    `json:"source"` // "asr" or "manual"
}

// VoiceEntry handles POST /api/finance/voice-entry.
// This endpoint accepts audio data, transcribes it using ASR, and returns a transaction draft.
// If ASR fails, it falls back to manual input with 100% reliability.
func (h *VoiceHandler) VoiceEntry(c *gin.Context) {
	var req VoiceEntryRequest

	// Parse form data (for multipart file upload)
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	// Get uploaded audio file
	file, err := c.FormFile("audio")
	if err != nil {
		// Fallback to manual mode if no audio file
		draft := h.createManualDraft(req)
		c.JSON(http.StatusOK, VoiceEntryResponse{
			Draft:      draft,
			Transcript: req.Description,
			Source:     "manual",
		})
		return
	}

	// Read audio file
	audioBytes, err := file.Open()
	if err != nil {
		// Fallback to manual mode on file read error
		draft := h.createManualDraft(req)
		c.JSON(http.StatusOK, VoiceEntryResponse{
			Draft:      draft,
			Transcript: req.Description,
			Source:     "manual",
		})
		return
	}
	defer audioBytes.Close()

	// Read all bytes
	buf := make([]byte, file.Size)
	_, err = audioBytes.Read(buf)
	if err != nil {
		// Fallback to manual mode on read error
		draft := h.createManualDraft(req)
		c.JSON(http.StatusOK, VoiceEntryResponse{
			Draft:      draft,
			Transcript: req.Description,
			Source:     "manual",
		})
		return
	}

	// Try ASR transcription
	transcript, err := h.asrAdapter.TranscribeVoice(c.Request.Context(), buf)
	if err != nil || transcript == "" {
		// 100% fallback to manual input on ASR failure
		draft := h.createManualDraft(req)
		c.JSON(http.StatusOK, VoiceEntryResponse{
			Draft:      draft,
			Transcript: req.Description,
			Source:     "manual",
		})
		return
	}

	// Parse the transcript to create a transaction draft
	draft, parseErr := h.parseTranscriptToDraft(transcript, req)
	if parseErr != nil {
		// If parsing fails, fall back to manual mode
		draft = h.createManualDraft(req)
		c.JSON(http.StatusOK, VoiceEntryResponse{
			Draft:      draft,
			Transcript: transcript,
			Source:     "manual",
		})
		return
	}

	// Success - return parsed draft
	c.JSON(http.StatusOK, VoiceEntryResponse{
		Draft:      draft,
		Transcript: transcript,
		Source:     "asr",
	})
}

// createManualDraft creates a draft transaction from manual input.
func (h *VoiceHandler) createManualDraft(req VoiceEntryRequest) *model.FinanceTransaction {
	now := time.Now()
	description := req.Description
	if description == "" {
		description = "手动输入"
	}

	return &model.FinanceTransaction{
		ID:          generateUUID(),
		FamilyID:    req.FamilyID,
		Type:        "expense", // Default to expense for manual entry
		AmountCents: 0,         // Requires user to fill in
		AccountID:   req.AccountID,
		OccurredAt:  now,
		Description: description,
		Version:     1,
	}
}

// parseTranscriptToDraft parses ASR transcript into a transaction draft.
// Expected format: "餐饮支出 50 元" or similar patterns.
func (h *VoiceHandler) parseTranscriptToDraft(transcript string, req VoiceEntryRequest) (*model.FinanceTransaction, error) {
	// Extract amount using regex (matches patterns like "50元", "50.5元", "五十元")
	amountRegex := regexp.MustCompile(`(\d+\.?\d*)\s*元`)
	matches := amountRegex.FindStringSubmatch(transcript)

	var amountCents int64
	if len(matches) > 1 {
		amount, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			// Convert yuan to cents and make negative for expense
			amountCents = -int64(amount * 100)
		}
	}

	// Determine type based on keywords
	transType := "expense"
	if containsAny(transcript, []string{"收入", "收款", "进账"}) {
		transType = "income"
		amountCents = -amountCents // Make positive for income
	} else if containsAny(transcript, []string{"转账", "转出"}) {
		transType = "transfer"
	}

	now := time.Now()

	draft := &model.FinanceTransaction{
		ID:          generateUUID(),
		FamilyID:    req.FamilyID,
		Type:        transType,
		AmountCents: amountCents,
		AccountID:   req.AccountID,
		OccurredAt:  now,
		Description: transcript,
		Version:     1,
	}

	// Try to extract category from transcript
	category := h.extractCategory(transcript)
	if category != "" {
		// Note: In a real implementation, we would look up the category ID
		// For now, we just store the category name in the description
		draft.Description = fmt.Sprintf("%s [%s]", transcript, category)
	}

	return draft, nil
}

// extractCategory tries to extract expense category from transcript.
func (h *VoiceHandler) extractCategory(transcript string) string {
	categories := map[string]string{
		"餐饮": "餐饮",
		"交通": "交通",
		"购物": "购物",
		"娱乐": "娱乐",
		"医疗": "医疗",
		"教育": "教育",
		"住房": "住房",
		"水电": "水电",
	}

	for keyword, category := range categories {
		if contains(transcript, keyword) {
			return category
		}
	}

	return ""
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

// findSubstring is a helper to check substring presence.
func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// containsAny checks if a string contains any of the given substrings.
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if contains(s, substr) {
			return true
		}
	}
	return false
}
