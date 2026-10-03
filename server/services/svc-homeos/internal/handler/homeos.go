// Package handler provides HTTP handlers for the homeos service.
package handler

import (
	"fmt"
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

// MembersSnapshotResponse represents the response for GET /members/snapshot.
// Per PRD 15.6 and tech plan §4.2: internal endpoint for authz SDK and service projections.
// P1: family_overrides and object_acls always return empty arrays (定版 ㉖).
type MembersSnapshotResponse struct {
	PVersion       int64            `json:"pver"`
	Members        []MemberInfo     `json:"members"`
	Permissions    []PermissionInfo `json:"permissions"`
	FamilyOverrides []interface{}   `json:"family_overrides"` // P1: always empty
	ObjectACLs     []interface{}    `json:"object_acls"`      // P1: always empty
}

// MemberInfo represents a member in the snapshot response.
type MemberInfo struct {
	MemberID string  `json:"member_id"`
	UserID   *string `json:"user_id"`
	Name     string  `json:"name"`
	Relation string  `json:"relation"`
	Role     string  `json:"role"` // owner, member, ward, guest
	Avatar   *string `json:"avatar"`
}

// PermissionInfo represents a permission entry in the snapshot.
type PermissionInfo struct {
	Scope     string `json:"scope"`     // module, data, operation
	Resource  string `json:"resource"`
	Action    string `json:"action"`
	Condition string `json:"condition"`
}

// GetMembersSnapshot handles GET /api/homeos/members/snapshot.
// Internal endpoint for authz SDK and cross-service read-only access.
// Supports If-None-Match: pver for 304 Not Modified.
func GetMembersSnapshot(c *gin.Context, db *gorm.DB) {
	familyID := c.Query("fid")
	if familyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fid query parameter is required"})
		return
	}

	// Get family to retrieve pver
	family, err := repo.GetFamily(c.Request.Context(), db, familyID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "family not found"})
		return
	}

	// Check If-None-Match header for conditional request
	ifNoneMatch := c.GetHeader("If-None-Match")
	if ifNoneMatch != "" {
		// Parse the pver from If-None-Match header
		var cachedPVer int64
		if _, err := fmt.Sscanf(ifNoneMatch, "%d", &cachedPVer); err == nil {
			if cachedPVer >= family.PVersion {
				c.Status(http.StatusNotModified)
				return
			}
		}
	}

	// List members for this family
	members, err := repo.ListMembers(c.Request.Context(), db, familyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list members: " + err.Error()})
		return
	}

	// Build member info array
	memberInfos := make([]MemberInfo, 0, len(members))
	for _, m := range members {
		relation := ""
		if m.Relation != nil {
			relation = *m.Relation
		}
		memberInfos = append(memberInfos, MemberInfo{
			MemberID: m.MemberID,
			UserID:   m.UserID,
			Name:     m.Name,
			Relation: relation,
			Role:     m.Role,
			Avatar:   m.Avatar,
		})
	}

	// Build permissions based on role-default matrix (PRD 15.3)
	// P1: Return static permission set based on authz policy matrix
	permissions := []PermissionInfo{
		{Scope: "module", Resource: "homeos:governance", Action: "read", Condition: "all"},
		{Scope: "module", Resource: "homeos:module_config", Action: "read", Condition: "all"},
		{Scope: "module", Resource: "homeos:time_collab", Action: "read", Condition: "all"},
		{Scope: "module", Resource: "homeos:module_config", Action: "update", Condition: "owner"},
		{Scope: "data", Resource: "finance", Action: "read", Condition: "all"},
		{Scope: "operation", Resource: "finance", Action: "create", Condition: "owner,member"},
	}

	response := MembersSnapshotResponse{
		PVersion:        family.PVersion,
		Members:         memberInfos,
		Permissions:     permissions,
		FamilyOverrides: []interface{}{}, // P1: always empty
		ObjectACLs:      []interface{}{}, // P1: always empty
	}

	c.Header("ETag", fmt.Sprintf("%d", family.PVersion))
	c.JSON(http.StatusOK, response)
}

// AppBundlesResponse represents the response for GET /app/bundles.
type AppBundlesResponse struct {
	Bundles []BundleInfo `json:"bundles"`
}

// BundleInfo represents a frontend bundle's metadata.
type BundleInfo struct {
	Code       string `json:"code"`
	Version    string `json:"version"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Size       int    `json:"size"`
	MinCompat  string `json:"min_compat"`
}

// GetAppBundles handles GET /api/homeos/app/bundles.
// Returns frontend bundle metadata for on-demand loading and remote distribution.
func GetAppBundles(c *gin.Context) {
	// P1: Return stub data for implemented faces (homeos + finance)
	// In production, this would read from a bundles table or configuration
	bundles := []BundleInfo{
		{
			Code:      "homeos",
			Version:   "1.0.0",
			URL:       "/static/bundles/homeos-1.0.0.js",
			SHA256:    "placeholder_sha256_homeos",
			Size:      1024000, // ~1MB
			MinCompat: "1.0.0",
		},
		{
			Code:      "finance",
			Version:   "1.0.0",
			URL:       "/static/bundles/finance-1.0.0.js",
			SHA256:    "placeholder_sha256_finance",
			Size:      850000, // ~850KB
			MinCompat: "1.0.0",
		},
	}

	c.JSON(http.StatusOK, AppBundlesResponse{Bundles: bundles})
}

// AppVersionResponse represents the response for GET /app/version.
type AppVersionResponse struct {
	CurrentVersion string `json:"current_version"`
	UpdateNotes    string `json:"update_notes"`
	MinCompat      string `json:"min_compat"`
	ForceUpdate    bool   `json:"force_update"`
}

// GetAppVersion handles GET /api/homeos/app/version.
// Silent version check after login. Returns current version + update notes + min_compat.
// Still goes through middleware auth (with token), but response contains no family data.
func GetAppVersion(c *gin.Context) {
	// P1: Return static version info
	// In production, this would be configurable via admin panel or config file
	c.JSON(http.StatusOK, AppVersionResponse{
		CurrentVersion: "1.0.0",
		UpdateNotes:    "P1-M2 release: Complete financial management, loan tracking, split settlements",
		MinCompat:      "1.0.0",
		ForceUpdate:    false,
	})
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
