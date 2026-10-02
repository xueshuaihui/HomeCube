// Package handler provides HTTP handlers for the homeos service.
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// GetPrivacyPolicy handles GET /api/homeos/legal/privacy-policy.
func GetPrivacyPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"title":   "隐私政策",
		"content": "【占位】本文档尚未定版，商用前将由法务部门提供正式文本。",
		"version": "P1-M2 收口期",
		"updated_at": time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
	})
}

// GetUserAgreement handles GET /api/homeos/legal/user-agreement.
func GetUserAgreement(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"title":   "用户协议",
		"content": "【占位】本文档尚未定版，商用前将由法务部门提供正式文本。",
		"version": "P1-M2 收口期",
		"updated_at": time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
	})
}
