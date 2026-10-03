// Package handler provides authentication handlers for the homeos service.
package handler

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// SMSCodeRequest represents the request to send an SMS verification code.
type SMSCodeRequest struct {
	Phone string `json:"phone" binding:"required"`
}

// SMSCodeResponse represents the response after sending SMS code.
type SMSCodeResponse struct {
	Code      string `json:"code"`       // For testing only, remove in production
	ExpiresIn int    `json:"expires_in"` // seconds
	Message   string `json:"message"`
}

// SendSMSCode handles POST /api/homeos/auth/sms-code.
// Per PRD 3.4.1: SMS verification code login with rate limiting.
func SendSMSCode(c *gin.Context, db *gorm.DB) {
	var req SMSCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// P1-M1 stub: Generate fixed test code for development
	// In production, integrate with SMS provider (Aliyun/Tencent per DEPENDENCIES.md)
	testCode := "123456"

	// Store code in database with expiry (for production use Redis or similar)
	// For P1 stub, we just return the code directly
	c.JSON(http.StatusOK, SMSCodeResponse{
		Code:      testCode,
		ExpiresIn: 300, // 5 minutes
		Message:   "验证码已发送（测试模式：123456）",
	})
}

// LoginRequest represents the login request.
type LoginRequest struct {
	Phone     string `json:"phone" binding:"required"`
	SMSCode   string `json:"sms_code" binding:"required"`
	FamilyID  string `json:"family_id,omitempty"` // Optional: switch family during login
}

// LoginResponse represents the login response with JWT tokens.
type LoginResponse struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	User         UserClaims `json:"user"`
	Families     []FamilyInfo `json:"families"`
}

// UserClaims represents the user information in JWT claims.
type UserClaims struct {
	ID       string `json:"id"`
	Phone    string `json:"phone"`
	Name     string `json:"name,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
}

// FamilyInfo represents basic family information.
type FamilyInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"` // owner/member/ward/guest
}

// Login handles POST /api/homeos/auth/login.
// Per PRD 3.4.1 & 15.6: Exchange SMS code for JWT token with family_id, role, pver.
func Login(c *gin.Context, db *gorm.DB) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// P1-M1 stub: Verify SMS code (in production, check against stored code)
	if req.SMSCode != "123456" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "验证码错误"})
		return
	}

	// Find or create user by phone
	// For P1 stub, create a mock user
	userID := fmt.Sprintf("user-%s", req.Phone)

	// Find user's families and roles
	// For P1 stub, return mock data
	families := []FamilyInfo{
		{
			ID:   "family-001",
			Name: "示例家庭",
			Role: "owner",
		},
	}

	// If no family specified, use first one
	selectedFamilyID := req.FamilyID
	if selectedFamilyID == "" && len(families) > 0 {
		selectedFamilyID = families[0].ID
	}

	// Generate JWT tokens
	accessToken, refreshToken, err := generateTokens(userID, selectedFamilyID, families[0].Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User: UserClaims{
			ID:    userID,
			Phone: req.Phone,
		},
		Families: families,
	})
}

// RefreshRequest represents the refresh token request.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// RefreshResponse represents the refresh token response.
type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// Refresh handles POST /api/homeos/auth/refresh.
// Per PRD 3.4.1: Rotate refresh token every 30 days.
func Refresh(c *gin.Context, db *gorm.DB) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Parse and validate refresh token
	token, err := jwt.Parse(req.RefreshToken, func(token *jwt.Token) (interface{}, error) {
		// In production, load private key from secure storage
		return []byte("test-secret-key-change-in-production"), nil
	})

	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token claims"})
		return
	}

	userID, _ := claims["sub"].(string)
	familyID, _ := claims["fid"].(string)
	role, _ := claims["role"].(string)

	// Generate new tokens
	accessToken, newRefreshToken, err := generateTokensWithRole(userID, familyID, role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, RefreshResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
	})
}

// generateTokens generates access and refresh tokens.
func generateTokens(userID, familyID, role string) (string, string, error) {
	return generateTokensWithRoleAndPVer(userID, familyID, role, 1)
}

// generateTokensWithRole generates tokens with role claim.
func generateTokensWithRole(userID, familyID, role string) (string, string, error) {
	return generateTokensWithRoleAndPVer(userID, familyID, role, 1)
}

// generateTokensWithRoleAndPVer generates JWT tokens with all required claims.
// Per PRD 3.4.1 & 15.6: token carries family_id, role snapshot, and pver.
func generateTokensWithRoleAndPVer(userID, familyID, role string, pver int64) (string, string, error) {
	// Access token: 15 minutes
	accessTokenClaims := jwt.MapClaims{
		"sub":   userID,
		"fid":   familyID,
		"role":  role,
		"pver":  pver,
		"jti":   generateJTI(),
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(15 * time.Minute).Unix(),
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, accessTokenClaims)
	// For P1 stub, use HS256 instead of RS256 (production must use RS256)
	accessToken.Header["alg"] = "HS256"
	accessTokenString, err := accessToken.SignedString([]byte("test-secret-key-change-in-production"))
	if err != nil {
		return "", "", err
	}

	// Refresh token: 30 days
	refreshTokenClaims := jwt.MapClaims{
		"sub":   userID,
		"fid":   familyID,
		"role":  role,
		"pver":  pver,
		"jti":   generateJTI(),
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(30 * 24 * time.Hour).Unix(),
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodRS256, refreshTokenClaims)
	refreshToken.Header["alg"] = "HS256"
	refreshTokenString, err := refreshToken.SignedString([]byte("test-secret-key-change-in-production"))
	if err != nil {
		return "", "", err
	}

	return accessTokenString, refreshTokenString, nil
}

// generateJTI generates a unique JWT ID.
func generateJTI() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("jti-%d-%d", time.Now().UnixNano(), n.Int64())
}

// Logout handles POST /api/homeos/auth/logout.
// Per PRD 3.4.1: Revoke refresh token.
func Logout(c *gin.Context, db *gorm.DB) {
	// In production, add refresh token to blacklist
	// For P1 stub, just return success
	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}
