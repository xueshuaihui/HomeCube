package authz

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents the JWT claims structure per PRD 15.6 and tech plan §4.1
type Claims struct {
	Subject  string `json:"sub"`  // account_id
	FamilyID string `json:"fid"`  // family_id for current session
	Role     string `json:"role"` // owner|member|ward|guest
	PVersion int64  `json:"pver"` // permission version
	JTI      string `json:"jti"`  // session id
	jwt.RegisteredClaims
}

// Role constants per PRD 15.1
const (
	RoleOwner  = "owner"
	RoleMember = "member"
	RoleWard   = "ward"
	RoleGuest  = "guest"
)

// Scope constants per PRD 15.2
const (
	ScopeModule = "module"
	ScopeData   = "data"
	ScopeAction = "action"
)

// Action constants
const (
	ActionRead   = "read"
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionDelete = "delete"
	ActionExport = "export"
	ActionInvite = "invite"
	ActionAdmin  = "admin"
)

// System codes per PRD 16.1
const (
	SystemHomeOS   = "homeos"
	SystemFinance  = "finance"
	SystemPurchase = "purchase"
	SystemDiet     = "diet"
	SystemTrip     = "trip"
	SystemKin      = "kin"
	SystemGrowth   = "growth"
)

// Resource types for HomeOS sub-systems (PRD 15.3 rows 1-3)
const (
	ResourceHomeOSGovernance   = "homeos:governance"    // members/permissions/settings
	ResourceHomeOSModuleConfig = "homeos:module_config" // face configuration
	ResourceHomeOSTimeCollab   = "homeos:time_collab"   // calendar/todo/reminder/board/vote/dynamic
)

// PermissionLevel represents the permission level for a role-resource-action combination
type PermissionLevel string

const (
	PermAll       PermissionLevel = "A" // All (read/write/delete/export)
	PermMine      PermissionLevel = "M" // Mine only (self-related)
	PermRead      PermissionLevel = "R" // Read only
	PermNone      PermissionLevel = "-" // No permission
	PermOwnerOnly PermissionLevel = "O" // Owner only
)

// PermissionMatrix is the built-in 9-row × 5-role matrix from PRD 15.3
// Rows: 7 systems (HomeOS occupies 3 rows)
// Columns: 5 roles (owner, member, ward, ward_no_account, guest)
var PermissionMatrix = map[string]map[string]PermissionLevel{
	// Row 1: HomeOS (members/permissions/settings) - PRD 15.3 row 1
	ResourceHomeOSGovernance: {
		RoleOwner:         PermAll,
		RoleMember:        PermRead, // Member list read + write own ward's basic fields
		RoleWard:          PermNone,
		"ward_no_account": PermNone, // Written by guardian via on_behalf_of
		RoleGuest:         PermNone,
	},

	// Row 2: HomeOS · Module Configuration (face directory & family module config) - PRD 15.3 row 2
	ResourceHomeOSModuleConfig: {
		RoleOwner:         PermAll,
		RoleMember:        PermRead,
		RoleWard:          PermRead,
		"ward_no_account": PermNone, // No login entry
		RoleGuest:         PermRead,
	},

	// Row 3: HomeOS · Time & Collaboration Objects (calendar/todo/reminder/rules/board/vote/dynamic) - PRD 15.3 row 3 (定版 ㉓)
	ResourceHomeOSTimeCollab: {
		RoleOwner:         PermAll,
		RoleMember:        PermMine, // Create/edit own, check complete, respond to others, cannot delete others'
		RoleWard:          PermMine, // Create own todo/reminder/message, check complete, cannot delete others'
		"ward_no_account": PermMine, // Written by guardian via on_behalf_of
		RoleGuest:         PermMine, // Message + check complete, cannot delete
	},

	// Row 4: Finance - PRD 15.3 row 4
	SystemFinance: {
		RoleOwner:         PermAll,
		RoleMember:        PermMine, // M write + R full family read; export requires admin authorization
		RoleWard:          PermNone,
		"ward_no_account": PermNone, // Recorded only (no write)
		RoleGuest:         PermNone,
	},

	// Row 5: Purchase - PRD 15.3 row 5
	SystemPurchase: {
		RoleOwner:         PermAll,
		RoleMember:        PermAll,  // Lists and inventory are family-shared objects
		RoleWard:          PermMine, // Can check complete, cannot delete
		"ward_no_account": PermNone,
		RoleGuest:         PermRead, // Read + check
	},

	// Row 6: Diet - PRD 15.3 row 6
	SystemDiet: {
		RoleOwner:         PermAll,
		RoleMember:        PermAll,  // Recipes/menus; records only for self
		RoleWard:          PermRead, // Read + be ordered
		"ward_no_account": PermNone, // Recorded (dietary restrictions/records by guardian)
		RoleGuest:         PermRead,
	},

	// Row 7: Trip - PRD 15.3 row 7
	SystemTrip: {
		RoleOwner:         PermAll,
		RoleMember:        PermMine, // M write + R full family trips
		RoleWard:          PermRead, // Own trips
		"ward_no_account": PermNone, // Recorded (pickup/dropoff targets)
		RoleGuest:         PermRead,
	},

	// Row 8: Kin (Family Members) - PRD 15.3 row 8
	SystemKin: {
		RoleOwner:         PermAll,
		RoleMember:        PermMine, // Own profile; R non-private fields of others
		RoleWard:          PermNone,
		"ward_no_account": PermNone, // Profile written by guardian
		RoleGuest:         PermRead, // Whitelist fields
	},

	// Row 9: Growth - PRD 15.3 row 9
	SystemGrowth: {
		RoleOwner:         PermAll,
		RoleMember:        PermMine, // Own; R own ward objects
		RoleWard:          PermMine, // Record self
		"ward_no_account": PermNone, // Records written by guardian
		RoleGuest:         PermNone,
	},
}

// VisibleFieldsMap defines field-level visibility per PRD 15.4
// Key: resource, Value: map of role to visible fields
var VisibleFieldsMap = map[string]map[string][]string{
	// Member fields (PRD 15.4)
	"member": {
		RoleOwner:  {"id", "family_id", "user_id", "name", "relation", "role", "avatar", "status", "guardian_id", "birthday", "blood_type", "allergies", "emergency_contact", "size", "gift_preferences", "wishes", "color_brand"},
		RoleMember: {"id", "family_id", "name", "relation", "avatar", "status"}, // Non-private fields
		RoleWard:   {"id", "name", "avatar"},                                    // Limited view
		RoleGuest:  {"id", "name", "avatar"},                                    // Whitelist fields
	},

	// Finance transaction fields (PRD 15.4 + privacy default)
	"finance_transaction": {
		RoleOwner:  {"id", "family_id", "amount_cents", "type", "category_id", "account_id", "occurred_at", "description", "author_id", "is_private", "tags", "receipt_file_id"},
		RoleMember: {"id", "family_id", "amount_cents", "type", "category_id", "occurred_at", "description", "author_id", "tags"}, // Cannot see is_private flag or receipt of others' private transactions
		RoleWard:   {},                                                                                                            // No access
		RoleGuest:  {},                                                                                                            // No access
	},

	// Growth record fields (L3 protection per PRD 15.4, 20.3)
	"growth_record": {
		RoleOwner:  {"id", "family_id", "member_id", "type", "value", "photos", "notes", "created_at"},
		RoleMember: {"id", "family_id", "member_id", "type", "value", "created_at"}, // Photos may be L3
		RoleWard:   {"id", "type", "value"},                                         // Limited
		RoleGuest:  {},                                                              // No access
	},
}

// Verify validates a JWT token using RS256 and returns the claims
// Implements PRD 15.6 and tech plan §4.1
func Verify(tokenString string, publicKey *rsa.PublicKey) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return publicKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		// Validate required claims
		if claims.Subject == "" {
			return nil, fmt.Errorf("missing subject (account_id)")
		}
		if claims.FamilyID == "" {
			return nil, fmt.Errorf("missing fid (family_id)")
		}
		if claims.Role == "" {
			return nil, fmt.Errorf("missing role")
		}

		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// canByMatrix checks the role-default matrix (PRD 15.3)
// Returns the permission level and whether the action is allowed at the role level
func canByMatrix(role, resource, action string) (PermissionLevel, bool) {
	level, exists := PermissionMatrix[resource][role]
	if !exists {
		return PermNone, false
	}

	allowed := false
	switch level {
	case PermAll:
		allowed = true // All actions allowed
	case PermOwnerOnly:
		allowed = (role == RoleOwner)
	case PermRead:
		allowed = (action == ActionRead)
	case PermMine:
		// Mine means can perform actions on own objects
		// For module scope, this translates to read/create/update but not delete
		switch action {
		case ActionRead, ActionCreate, ActionUpdate:
			allowed = true
		case ActionDelete, ActionExport:
			allowed = false
		default:
			allowed = false
		}
	case PermNone:
		allowed = false
	default:
		allowed = false
	}

	return level, allowed
}

// GetObjectAuthorID extracts author_id or owner_id from an object using reflection
// Returns empty string if no such field exists or if obj is nil
func GetObjectAuthorID(obj interface{}) string {
	if obj == nil {
		return ""
	}

	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return ""
	}

	// Try AuthorID first, then OwnerID
	for _, fieldName := range []string{"AuthorID", "OwnerID", "author_id", "owner_id"} {
		field := v.FieldByName(fieldName)
		if field.IsValid() && field.Kind() == reflect.String {
			return field.String()
		}
	}

	return ""
}

// hasOwnershipField checks if a resource type has ownership fields (author_id or owner_id)
// Resources without ownership fields (like governance) should skip ownership checks
func hasOwnershipField(resource string) bool {
	// Governance resources don't have individual object ownership
	if strings.HasPrefix(resource, "homeos:governance") {
		return false
	}
	// Module config is family-level, not per-object
	if resource == "homeos:module_config" {
		return false
	}
	// Other resources (finance, growth, kin, etc.) have ownership
	return true
}

// Can performs permission checking following PRD 15.2 order:
// explicit deny > role-default matrix > family-level overrides (P1 placeholder) > object-level ACL (P1 placeholder)
//
// Parameters:
// - ctx: context for potential future extensions
// - scope: "module" | "data" | "action" (PRD 15.2)
// - resource: system code or resource identifier (e.g., "finance", "homeos:module_config")
// - action: "read" | "create" | "update" | "delete" | "export" | "invite" | "admin"
// - obj: optional object for object-level checks (P1 placeholder)
//
// Returns true if the action is permitted, false otherwise
func Can(ctx context.Context, claims *Claims, scope string, resource string, action string, obj interface{}) bool {
	if claims == nil {
		return false
	}

	// Step 1: Explicit deny (P1: check for explicit deny rules - currently none in P1)
	// TODO(P6): Implement explicit deny rules from Permission table

	// Step 2: Role-default matrix (PRD 15.3) - This is the main P1 implementation
	level, allowed := canByMatrix(claims.Role, resource, action)
	
	// For PermMine, check object ownership per PRD 15.3: "M 只能操作自己创建的对象"
	if level == PermMine && allowed && hasOwnershipField(resource) {
		objAuthorID := GetObjectAuthorID(obj)
		// If object has an author/owner and it's not the current user, deny access
		if objAuthorID != "" && objAuthorID != claims.Subject {
			return false
		}
	}
	
	if allowed {
		return true
	}

	// Step 3: Family-level overrides (P1 placeholder - always returns false in P1 per 定版 ㉖)
	// TODO(P6): Implement family-level overrides from homeos_family_module and Permission table
	// P1: family_overrides array is always empty in snapshot response

	// Step 4: Object-level ACL (P1 placeholder - always returns false in P1 per 定版 ㉖)
	// TODO(P6): Implement object-level ACL
	// P1: object_acls array is always empty in snapshot response

	return false
}

// VisibleFields returns the list of visible fields for a given role and resource
// Implements PRD 15.4 field-level visibility
func VisibleFields(role string, resource string) []string {
	fields, exists := VisibleFieldsMap[resource]
	if !exists {
		return nil
	}

	visibleFields, exists := fields[role]
	if !exists {
		return nil
	}

	return visibleFields
}

// LoadRSAPublicKey loads an RSA public key from a PEM file
func LoadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key file: %w", err)
	}

	publicKey, err := jwt.ParseRSAPublicKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse RSA public key: %w", err)
	}

	return publicKey, nil
}

// IsUnbornSystem checks if a system is not yet born (not implemented)
// Per PRD 15.3 and tech plan §4.3, unborn systems should return "拒绝访问 + 未出生"
func IsUnbornSystem(systemCode string) bool {
	// P1: Only homeos and finance are implemented
	// Unborn systems: purchase, diet, trip, kin, growth
	unbornSystems := map[string]bool{
		SystemPurchase: true,
		SystemDiet:     true,
		SystemTrip:     true,
		SystemKin:      true,
		SystemGrowth:   true,
	}
	return unbornSystems[systemCode]
}

// NormalizeResource normalizes a resource string to match the matrix keys
func NormalizeResource(systemCode string, subResource string) string {
	if systemCode == SystemHomeOS {
		switch subResource {
		case "governance", "members", "permissions", "settings":
			return ResourceHomeOSGovernance
		case "module_config", "modules":
			return ResourceHomeOSModuleConfig
		case "time_collab", "calendar", "todo", "reminder", "board", "vote", "dynamic":
			return ResourceHomeOSTimeCollab
		default:
			return ResourceHomeOSGovernance // Default to governance
		}
	}
	return systemCode
}

// MarshalClaims serializes claims to JSON for caching
func MarshalClaims(claims *Claims) ([]byte, error) {
	return json.Marshal(claims)
}

// UnmarshalClaims deserializes claims from JSON cache
func UnmarshalClaims(data []byte) (*Claims, error) {
	var claims Claims
	err := json.Unmarshal(data, &claims)
	if err != nil {
		return nil, err
	}
	return &claims, nil
}

// GetExpiry returns the token expiry time
func (c *Claims) GetExpiry() time.Time {
	if c.ExpiresAt != nil {
		return c.ExpiresAt.Time
	}
	return time.Time{}
}

// IsExpired checks if the token is expired
func (c *Claims) IsExpired() bool {
	if c.ExpiresAt == nil {
		return false
	}
	return time.Now().After(c.ExpiresAt.Time)
}

// String representation for debugging
func (c *Claims) String() string {
	return fmt.Sprintf("Claims{sub=%s, fid=%s, role=%s, pver=%d, jti=%s}",
		c.Subject, c.FamilyID, c.Role, c.PVersion, c.JTI)
}
