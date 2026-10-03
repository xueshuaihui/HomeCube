// Package handler provides family management handlers for the homeos service.
package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CreateFamilyRequest represents the request to create a new family.
type CreateFamilyRequest struct {
	Name     string `json:"name" binding:"required"`
	Timezone string `json:"timezone" binding:"required"` // e.g., "Asia/Shanghai"
	Currency string `json:"currency" binding:"required"` // e.g., "CNY"
	Avatar   string `json:"avatar,omitempty"`
}

// CreateFamilyResponse represents the response after creating a family.
type CreateFamilyResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// CreateFamily handles POST /api/homeos/families.
// Per PRD 14.5 #2 & 17.8: Create family and enforce module selection.
func CreateFamily(c *gin.Context, db *gorm.DB) {
	var req CreateFamilyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Get user ID from JWT claims
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Create family
	familyID := uuid.New().String()
	now := time.Now()

	// For P1 stub: Insert into database
	// In production, use proper GORM model and transaction
	family := map[string]interface{}{
		"id":        familyID,
		"name":      req.Name,
		"owner_id":  userID,
		"timezone":  req.Timezone,
		"currency":  req.Currency,
		"avatar":    req.Avatar,
		"created_at": now,
		"updated_at": now,
	}

	// Insert family record
	if err := db.Table("homeos_families").Create(family).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create family"})
		return
	}

	// Add creator as owner member
	member := map[string]interface{}{
		"id":         uuid.New().String(),
		"family_id":  familyID,
		"user_id":    userID,
		"role":       "owner",
		"created_at": now,
		"updated_at": now,
	}

	if err := db.Table("homeos_members").Create(member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}

	c.JSON(http.StatusCreated, CreateFamilyResponse{
		ID:      familyID,
		Name:    req.Name,
		Message: "家庭创建成功，请选择要启用的功能面",
	})
}

// AcceptInviteRequest represents the request to accept an invitation.
type AcceptInviteRequest struct {
	InviteCode string `json:"invite_code" binding:"required"`
}

// AcceptInviteResponse represents the response after accepting an invitation.
type AcceptInviteResponse struct {
	FamilyID   string `json:"family_id"`
	FamilyName string `json:"family_name"`
	Role       string `json:"role"`
	Message    string `json:"message"`
}

// AcceptInvite handles POST /api/homeos/family/invite/accept.
// Per PRD 3.4.1 & 18.2#1: Accept invitation and switch family context.
func AcceptInvite(c *gin.Context, db *gorm.DB) {
	var req AcceptInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Get user ID from JWT claims
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Find invitation by code
	var invite map[string]interface{}
	err := db.Table("homeos_invitations").Where("code = ? AND status = ?", req.InviteCode, "pending").First(&invite).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "邀请码无效或已过期"})
		return
	}

	familyID, _ := invite["family_id"].(string)
	role, _ := invite["role"].(string)

	// Add user as member
	now := time.Now()
	member := map[string]interface{}{
		"id":         uuid.New().String(),
		"family_id":  familyID,
		"user_id":    userID,
		"role":       role,
		"created_at": now,
		"updated_at": now,
	}

	if err := db.Table("homeos_members").Create(member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}

	// Mark invitation as accepted
	db.Table("homeos_invitations").Where("code = ?", req.InviteCode).Update("status", "accepted")

	// Get family name
	var family map[string]interface{}
	db.Table("homeos_families").Where("id = ?", familyID).First(&family)
	familyName, _ := family["name"].(string)

	c.JSON(http.StatusOK, AcceptInviteResponse{
		FamilyID:   familyID,
		FamilyName: familyName,
		Role:       role,
		Message:    "成功加入家庭",
	})
}

// ListFamiliesResponse represents the list of families for current user.
type ListFamiliesResponse struct {
	Families []FamilyInfo `json:"families"`
}

// ListFamilies handles GET /api/homeos/families.
// Returns all families the current user belongs to.
func ListFamilies(c *gin.Context, db *gorm.DB) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Query families through members table
	var families []FamilyInfo
	err := db.Table("homeos_families f").
		Select("f.id, f.name, m.role").
		Joins("JOIN homeos_members m ON f.id = m.family_id").
		Where("m.user_id = ?", userID).
		Scan(&families).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query families"})
		return
	}

	c.JSON(http.StatusOK, ListFamiliesResponse{
		Families: families,
	})
}

// SwitchFamilyRequest represents the request to switch current family.
type SwitchFamilyRequest struct {
	FamilyID string `json:"family_id" binding:"required"`
}

// SwitchFamilyResponse represents the response with new tokens after switching family.
type SwitchFamilyResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	FamilyID     string `json:"family_id"`
	Message      string `json:"message"`
}

// SwitchFamily handles POST /api/homeos/family/switch.
// Per PRD 14.5 #2: Switch family context, reissue token within 1 second.
func SwitchFamily(c *gin.Context, db *gorm.DB) {
	var req SwitchFamilyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Verify user is member of this family
	var member map[string]interface{}
	err := db.Table("homeos_members").Where("user_id = ? AND family_id = ?", userID, req.FamilyID).First(&member).Error
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "您不是该家庭成员"})
		return
	}

	role, _ := member["role"].(string)

	// Generate new tokens with new family_id
	accessToken, refreshToken, err := generateTokens(userID.(string), req.FamilyID, role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, SwitchFamilyResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		FamilyID:     req.FamilyID,
		Message:      "切换家庭成功",
	})
}
