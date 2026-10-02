package authz

import (
	"reflect"
	"strings"
	"unicode"
)

// FilterDTO filters DTO fields based on visible fields list
// Implements PRD 15.4 field-level visibility at DTO serialization layer
//
// Parameters:
// - dto: the DTO object to filter (must be a struct or pointer to struct)
// - visibleFields: list of field names that should be visible
//
// Returns a new interface{} with only the visible fields populated
// L3 fields are excluded from default export per PRD 20.1
func FilterDTO(dto interface{}, visibleFields []string) interface{} {
	if dto == nil {
		return nil
	}

	// Build a set of visible field names for O(1) lookup
	visibleSet := make(map[string]bool)
	for _, field := range visibleFields {
		visibleSet[strings.ToLower(field)] = true
	}

	v := reflect.ValueOf(dto)
	t := reflect.TypeOf(dto)

	// Handle pointer
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
		t = t.Elem()
	}

	// Must be a struct
	if v.Kind() != reflect.Struct {
		return dto
	}

	// Create a new struct of the same type
	result := reflect.New(t).Elem()

	// Copy only visible fields
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)

		// Use json tag as the primary match key, fallback to field name
		var matchKey string
		jsonTag := field.Tag.Get("json")
		if jsonTag != "" {
			// Handle json tag with options (e.g., "blood_type,omitempty")
			parts := strings.Split(jsonTag, ",")
			matchKey = strings.ToLower(parts[0])
		} else {
			// Fallback to snake_case conversion of field name
			matchKey = toSnakeCase(field.Name)
		}

		// Check if this field is in the visible set
		if visibleSet[matchKey] {
			result.Field(i).Set(v.Field(i))
		} else {
			// Set to zero value for non-visible fields
			result.Field(i).Set(reflect.Zero(field.Type))
		}
	}

	return result.Interface()
}

// IsL3Field checks if a field is classified as L3 (end-to-end encrypted)
// Per PRD 20.1, L3 fields should not enter default export or telemetry
func IsL3Field(fieldName string) bool {
	// L3 fields per PRD 20.1 and 15.4:
	// - Growth photos and body data
	// - Blood type, allergies, emergency contact
	// - Size, gift preferences, wishes, color brand
	l3Fields := map[string]bool{
		"photos":            true,
		"bloodtype":         true, // camelCase
		"blood_type":        true, // snake_case
		"allergies":         true,
		"emergencycontact":  true, // camelCase
		"emergency_contact": true, // snake_case
		"size":              true,
		"giftpreferences":   true, // camelCase
		"gift_preferences":  true, // snake_case
		"wishes":            true,
		"colorbrand":        true, // camelCase
		"color_brand":       true, // snake_case
		"bodydata":          true, // camelCase
		"body_data":         true, // snake_case
		"growthphotos":      true, // camelCase
		"growth_photos":     true, // snake_case
	}

	return l3Fields[strings.ToLower(fieldName)]
}

// FilterL3Fields removes L3 fields from a DTO
// Used for default exports and telemetry per PRD 20.1
func FilterL3Fields(dto interface{}) interface{} {
	if dto == nil {
		return nil
	}

	v := reflect.ValueOf(dto)
	t := reflect.TypeOf(dto)

	// Handle pointer
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
		t = t.Elem()
	}

	// Must be a struct
	if v.Kind() != reflect.Struct {
		return dto
	}

	// Create a new struct of the same type
	result := reflect.New(t).Elem()

	// Copy only non-L3 fields
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)

		// Skip L3 fields
		if IsL3Field(field.Name) {
			continue
		}

		result.Field(i).Set(v.Field(i))
	}

	return result.Interface()
}

// IsPrivateTransaction checks if a finance transaction has private flag set
// Implements PRD 15.3 financial privacy default: private transactions visible only to author and admin
func IsPrivateTransaction(dto interface{}) bool {
	if dto == nil {
		return false
	}

	v := reflect.ValueOf(dto)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return false
	}

	// Look for is_private or IsPrivate field
	privateField := v.FieldByName("IsPrivate")
	if !privateField.IsValid() {
		privateField = v.FieldByName("is_private")
	}

	if privateField.IsValid() && privateField.Kind() == reflect.Bool {
		return privateField.Bool()
	}

	return false
}

// CanViewPrivateTransaction checks if the current user can view a private transaction
// Per PRD 15.3: only author and admin can view private transactions
func CanViewPrivateTransaction(role string, authorID string, currentAccountID string) bool {
	// Admin (owner) can always view
	if role == RoleOwner {
		return true
	}

	// Author can view their own private transactions
	if authorID == currentAccountID {
		return true
	}

	// Others cannot view private transactions
	return false
}

// toSnakeCase converts PascalCase to snake_case
// e.g., "BloodType" -> "blood_type", "EmergencyContact" -> "emergency_contact", "ID" -> "id"
func toSnakeCase(s string) string {
	if len(s) == 0 {
		return s
	}

	var result strings.Builder
	runes := []rune(s)

	for i, r := range runes {
		if unicode.IsUpper(r) {
			// Check if this is part of an acronym (consecutive uppercase letters)
			isAcronym := i > 0 && unicode.IsUpper(runes[i-1])
			isLastChar := i == len(runes)-1
			isNextLower := !isLastChar && i < len(runes)-1 && unicode.IsLower(runes[i+1])

			// Add underscore before uppercase letter if:
			// - it's not the first character AND
			// - previous char is lowercase OR
			// - it's followed by a lowercase char (end of acronym)
			if i > 0 && (!isAcronym || isNextLower) {
				result.WriteRune('_')
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// GetStructFieldNames returns all field names of a struct
// Useful for debugging and testing
func GetStructFieldNames(dto interface{}) []string {
	if dto == nil {
		return nil
	}

	v := reflect.ValueOf(dto)
	t := reflect.TypeOf(dto)

	// Handle pointer
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
		t = t.Elem()
	}

	if v.Kind() != reflect.Struct {
		return nil
	}

	fields := make([]string, v.NumField())
	for i := 0; i < v.NumField(); i++ {
		fields[i] = t.Field(i).Name
	}

	return fields
}
