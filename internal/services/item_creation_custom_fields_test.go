//go:build test

package services

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"windshift/internal/testutils"
	"windshift/internal/validation"
)

// EncodeCustomFieldValuesJSON is the shared validation+encoding step for
// creation entry points. It must return an empty string for a nil map so
// callers skip the field, encode normalized values, and surface the same
// *validation.ValidationError the update path uses.
func TestEncodeCustomFieldValuesJSON(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	textKey := insertServiceCustomField(t, tdb, "Encode text", "text", "")
	numberKey := insertServiceCustomField(t, tdb, "Encode number", "number", "")
	dateKey := insertServiceCustomField(t, tdb, "Encode date", "date", "")

	t.Run("nil map encodes to empty string", func(t *testing.T) {
		got, err := EncodeCustomFieldValuesJSON(db, nil)
		if err != nil {
			t.Fatalf("EncodeCustomFieldValuesJSON: %v", err)
		}
		if got != "" {
			t.Fatalf("encoded = %q, want empty string", got)
		}
	})

	t.Run("normalizes and encodes supplied values", func(t *testing.T) {
		encoded, err := EncodeCustomFieldValuesJSON(db, map[string]any{
			textKey:   "  hello  ",
			numberKey: " 12.50 ",
		})
		if err != nil {
			t.Fatalf("EncodeCustomFieldValuesJSON: %v", err)
		}

		var decoded map[string]any
		if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
			t.Fatalf("decode encoded values: %v", err)
		}
		if decoded[textKey] != "hello" {
			t.Fatalf("encoded text = %#v, want trimmed %q", decoded[textKey], "hello")
		}
		if decoded[numberKey] != 12.5 {
			t.Fatalf("encoded number = %#v, want 12.5", decoded[numberKey])
		}
	})

	t.Run("invalid value returns validation error and no JSON", func(t *testing.T) {
		encoded, err := EncodeCustomFieldValuesJSON(db, map[string]any{dateKey: "28.02.2026"})
		if err == nil {
			t.Fatal("invalid date was accepted")
		}
		if encoded != "" {
			t.Fatalf("encoded = %q, want empty string on error", encoded)
		}
		var validationErr *validation.ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("error = %T %v, want *validation.ValidationError", err, err)
		}
		if validationErr.Field != "custom_field_values."+dateKey {
			t.Fatalf("field = %q, want custom_field_values.%s", validationErr.Field, dateKey)
		}
	})
}

// insertServiceCustomField inserts a custom_field_definitions row and returns
// its ID in the string-key shape custom-field value maps use.
func insertServiceCustomField(t *testing.T, tdb *testutils.TestDB, name, fieldType, options string) string {
	t.Helper()
	var id int
	err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type, options, required, display_order, system_default, created_at, updated_at)
		VALUES (?, ?, ?, FALSE, 1, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, name, fieldType, options).Scan(&id)
	if err != nil {
		t.Fatalf("insert custom field %q: %v", name, err)
	}
	return strconv.Itoa(id)
}
