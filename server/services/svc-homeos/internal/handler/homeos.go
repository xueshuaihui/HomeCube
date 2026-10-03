// Package handler provides HTTP handlers for the homeos service.
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
	"gorm.io/gorm"
)

// GetPrivacyPolicy handles GET /api/homeos/legal/privacy-policy.
func GetPrivacyPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"title":      "隐私政策",
		"content":    "【占位】本文档尚未定版，商用前将由法务部门提供正式文本。",
		"version":    "P1-M2 收口期",
		"updated_at": time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
	})
}

// GetUserAgreement handles GET /api/homeos/legal/user-agreement.
func GetUserAgreement(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"title":      "用户协议",
		"content":    "【占位】本文档尚未定版，商用前将由法务部门提供正式文本。",
		"version":    "P1-M2 收口期",
		"updated_at": time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
	})
}

// SearchRequest represents the query parameters for global search.
type SearchRequest struct {
	Q     string `form:"q" binding:"required"` // keyword to search
	Limit int    `form:"limit"`                // max results, default 20
}

// SearchResponse represents the search results.
type SearchResponse struct {
	Total   int                       `json:"total"`
	Results []model.HomeosSearchIndex `json:"results"`
}

// GetSearch handles GET /api/homeos/search?q={keyword}&limit=20.
// Per PRD 14.5 #7: global keyword search across all domains, indexed by svc-homeos.
func GetSearch(c *gin.Context, db *gorm.DB) {
	var req SearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: q parameter is required"})
		return
	}

	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Extract family_id from context (set by the auth middleware in main.go)
	familyID, exists := c.Get("family_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "family_id not found in context"})
		return
	}

	// Perform search
	results, err := repo.SearchByKeyword(c.Request.Context(), db, familyID.(string), req.Q, req.Limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}

	c.JSON(http.StatusOK, SearchResponse{
		Total:   len(results),
		Results: results,
	})
}
