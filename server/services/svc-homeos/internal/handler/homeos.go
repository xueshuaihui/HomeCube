// Package handler provides HTTP handlers for the homeos service.
package handler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
	"gorm.io/gorm"
)

// bundleManifestJSON is written by scripts/build-base.sh on every build (byte-identical to the
// published dist/releases/manifest.json) and compiled into the binary. The service therefore never
// reads the filesystem: what it advertises is exactly what was built and hash-verified at release
// time, and container working directories/permissions cannot change the answer (PRD 17.8).
//
//go:embed bundle_manifest.json
var bundleManifestJSON []byte

// bundleManifest is the single source of truth behind both /app/bundles and /app/version:
// PRD 12.4 requires the client's min_compat to come from the same place as the bundle manifest.
type bundleManifest struct {
	GeneratedAt string           `json:"generated_at"`
	Base        manifestRelease  `json:"base"`
	Bundles     []manifestBundle `json:"bundles"`
}

// manifestRelease describes the base (H5 shell) artifact.
type manifestRelease struct {
	Version     string `json:"version"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Size        int    `json:"size"`
	UpdateNotes string `json:"update_notes"`
	MinCompat   string `json:"min_compat"`
}

// manifestBundle describes one face's bundle. In P1 a bundle ships inside the base package
// (PRD 17.8 P1 row), so its url/sha256/size point at that package; P2 switches to one package per
// face without changing these fields.
type manifestBundle struct {
	Code      string `json:"code"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Size      int    `json:"size"`
	MinCompat string `json:"min_compat"`
}

var (
	semverPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// appManifest and appManifestErr hold the validated embedded manifest. A bad manifest is a build
// defect, so the endpoints answer 503 rather than serving a guessed version (the previous
// "fall back to 1.0.0" behaviour would advertise a hash that does not match any artifact).
var (
	appManifest    bundleManifest
	appManifestErr error
)

func init() {
	if err := json.Unmarshal(bundleManifestJSON, &appManifest); err != nil {
		appManifestErr = fmt.Errorf("解析 bundle_manifest.json: %w", err)
	} else {
		appManifestErr = appManifest.validate()
	}
	if appManifestErr != nil {
		slog.Error("分包清单不可用：/app/bundles 与 /app/version 将返回 503", "error", appManifestErr)
	}
}

// validate checks the manifest against the registry, which is the only authority on which domains
// have been born (PRD 16.1). Structural checks come first so a hand-edited manifest fails with a
// field name instead of a downstream nil/short string.
func (m bundleManifest) validate() error {
	if strings.TrimSpace(m.GeneratedAt) == "" {
		return fmt.Errorf("generated_at 为空")
	}
	if err := validateArtifact("base", m.Base.Version, m.Base.URL, m.Base.SHA256, m.Base.MinCompat, m.Base.Size); err != nil {
		return err
	}
	if strings.TrimSpace(m.Base.UpdateNotes) == "" {
		return fmt.Errorf("base.update_notes 为空")
	}

	seen := make(map[string]bool, len(m.Bundles))
	for i, b := range m.Bundles {
		label := fmt.Sprintf("bundles[%d]", i)
		if err := validateArtifact(label, b.Version, b.URL, b.SHA256, b.MinCompat, b.Size); err != nil {
			return err
		}
		if b.Code == "" {
			return fmt.Errorf("%s.code 为空", label)
		}
		if b.Code == registry.HomeosCode {
			return fmt.Errorf("%s.code 不能是 %q（主包不是分包）", label, registry.HomeosCode)
		}
		domain, ok := registry.ByCode(b.Code)
		if !ok {
			return fmt.Errorf("%s.code %q 不在 registry 域表里", label, b.Code)
		}
		if !domain.Born() {
			return fmt.Errorf("%s.code %q 尚未出生（出生期 %s，当期 %s）", label, b.Code, domain.BirthPhase, registry.CurrentPhase)
		}
		if seen[b.Code] {
			return fmt.Errorf("%s.code %q 重复", label, b.Code)
		}
		seen[b.Code] = true
	}

	// Set equality with registry.BundleSet() is the drift alarm: if a face is born (phase bumped,
	// pages.json updated) but the manifest was not regenerated, the shell would be told a face
	// exists with no entry. Refuse to serve instead of silently omitting it.
	want := registry.BundleSet()
	if len(want) != len(m.Bundles) {
		return fmt.Errorf("清单分面包数 %d 与 registry 当期分包数 %d 不一致（registry：%s）",
			len(m.Bundles), len(want), strings.Join(want, ","))
	}
	for _, code := range want {
		if !seen[code] {
			return fmt.Errorf("registry 当期分包 %q 不在清单里", code)
		}
	}
	return nil
}

func validateArtifact(label, version, url, sha256, minCompat string, size int) error {
	if !semverPattern.MatchString(version) {
		return fmt.Errorf("%s.version 不是 x.y.z：%q", label, version)
	}
	if !semverPattern.MatchString(minCompat) {
		return fmt.Errorf("%s.min_compat 不是 x.y.z：%q", label, minCompat)
	}
	if size <= 0 {
		return fmt.Errorf("%s.size 必须为正数：%d", label, size)
	}
	if !sha256Pattern.MatchString(sha256) {
		return fmt.Errorf("%s.sha256 不是 64 位小写十六进制：%q", label, sha256)
	}
	if !strings.HasPrefix(url, "/releases/") {
		return fmt.Errorf("%s.url 必须落在 /releases/ 下：%q", label, url)
	}
	if strings.Contains(url, "..") {
		return fmt.Errorf("%s.url 含路径穿越：%q", label, url)
	}
	return nil
}

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
	PVersion        int64            `json:"pver"`
	Members         []MemberInfo     `json:"members"`
	Permissions     []PermissionInfo `json:"permissions"`
	FamilyOverrides []interface{}    `json:"family_overrides"` // P1: always empty
	ObjectACLs      []interface{}    `json:"object_acls"`      // P1: always empty
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
	Scope     string `json:"scope"` // module, data, operation
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
	Code      string `json:"code"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Size      int    `json:"size"`
	MinCompat string `json:"min_compat"`
}

// GetAppBundles handles GET /api/homeos/app/bundles?codes=…&installed=….
//
// It is a pure snapshot of the embedded manifest, filtered by codes (PRD 3.7/17.8):
//   - no codes → every bundle born in the current phase, in registry order;
//   - unknown/unborn codes are ignored, the caller learns availability from /faces and
//     /family/modules, not from an error here;
//   - installed is accepted but not interpreted: the shell compares (code, version) itself
//     (navigation doc §6.11「比对本地 (code, version)」), which keeps this response cacheable.
//
// The base package is not a bundle entry; its version and min_compat come from /app/version.
func GetAppBundles(c *gin.Context) {
	if appManifestErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bundle_manifest_invalid"})
		return
	}

	requested := parseBundleCodes(c.Query("codes"))
	bundles := make([]BundleInfo, 0, len(appManifest.Bundles))
	for _, b := range appManifest.Bundles {
		if len(requested) > 0 && !requested[b.Code] {
			continue
		}
		bundles = append(bundles, BundleInfo{
			Code:      b.Code,
			Version:   b.Version,
			URL:       b.URL,
			SHA256:    b.SHA256,
			Size:      b.Size,
			MinCompat: b.MinCompat,
		})
	}

	c.JSON(http.StatusOK, AppBundlesResponse{Bundles: bundles})
}

// parseBundleCodes turns "finance, purchase" into a set; empty input yields an empty set, which
// the caller reads as "all bundles".
func parseBundleCodes(raw string) map[string]bool {
	out := make(map[string]bool)
	for _, code := range strings.Split(raw, ",") {
		if code = strings.TrimSpace(code); code != "" {
			out[code] = true
		}
	}
	return out
}

// AppVersionResponse represents the response for GET /app/version.
// PRD 3.4.8/12.4 fix the field set: current version + update notes + min_compat. Forced update is
// derived by the client from min_compat; there is no separate flag (㉕: min_compat is the only
// trigger).
type AppVersionResponse struct {
	CurrentVersion string `json:"current_version"`
	UpdateNotes    string `json:"update_notes"`
	MinCompat      string `json:"min_compat"`
}

// GetAppVersion handles GET /api/homeos/app/version.
// Silent version check after login. Returns current version + update notes + min_compat.
// Still goes through middleware auth (with token), but response contains no family data.
func GetAppVersion(c *gin.Context) {
	if appManifestErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bundle_manifest_invalid"})
		return
	}

	c.JSON(http.StatusOK, AppVersionResponse{
		CurrentVersion: appManifest.Base.Version,
		UpdateNotes:    appManifest.Base.UpdateNotes,
		MinCompat:      appManifest.Base.MinCompat,
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
