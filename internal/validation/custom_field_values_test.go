//go:build test

package validation

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"fmt"
	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestValidateAndNormalizeCustomFieldValues_NumberAndDate(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	numberKey := insertCustomField(t, tdb, "Estimate", "number", "")
	dateKey := insertCustomField(t, tdb, "Target date", "date", "")

	valid := []struct {
		name string
		key  string
		raw  any
		want any
	}{
		{name: "numeric string", key: numberKey, raw: " 12.50 ", want: float64(12.5)},
		{name: "JSON number", key: numberKey, raw: float64(-3.25), want: float64(-3.25)},
		{name: "Go integer", key: numberKey, raw: 7, want: float64(7)},
		{name: "blank number", key: numberKey, raw: "  ", want: nil},
		{name: "ISO date", key: dateKey, raw: " 2026-02-28 ", want: "2026-02-28"},
		{name: "blank date", key: dateKey, raw: "", want: nil},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			cfv := map[string]any{tt.key: tt.raw}
			if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
				t.Fatalf("ValidateAndNormalizeCustomFieldValues: %v", err)
			}
			if !reflect.DeepEqual(cfv[tt.key], tt.want) {
				t.Fatalf("normalized value = %#v (%T), want %#v (%T)", cfv[tt.key], cfv[tt.key], tt.want, tt.want)
			}
		})
	}

	invalid := []struct {
		name        string
		key         string
		raw         any
		wantMessage string
	}{
		{name: "number text", key: numberKey, raw: "abc", wantMessage: "number"},
		{name: "number boolean", key: numberKey, raw: true, wantMessage: "number"},
		{name: "number NaN", key: numberKey, raw: math.NaN(), wantMessage: "finite"},
		{name: "localized date", key: dateKey, raw: "28.02.2026", wantMessage: "YYYY-MM-DD"},
		{name: "impossible date", key: dateKey, raw: "2026-02-30", wantMessage: "YYYY-MM-DD"},
		{name: "timestamp", key: dateKey, raw: "2026-02-28T10:00:00Z", wantMessage: "YYYY-MM-DD"},
		{name: "numeric date", key: dateKey, raw: 20260228, wantMessage: "YYYY-MM-DD"},
	}
	for _, tt := range invalid {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			cfv := map[string]any{tt.key: tt.raw}
			err := ValidateAndNormalizeCustomFieldValues(db, cfv)
			if err == nil {
				t.Fatalf("invalid value %#v (%T) was accepted", tt.raw, tt.raw)
			}
			validationErr, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("error = %T %v, want *ValidationError", err, err)
			}
			if validationErr.Field != "custom_field_values."+tt.key {
				t.Fatalf("field = %q, want custom_field_values.%s", validationErr.Field, tt.key)
			}
			if !strings.Contains(validationErr.Message, tt.wantMessage) {
				t.Fatalf("message = %q, want it to contain %q", validationErr.Message, tt.wantMessage)
			}
		})
	}
}

// insertCustomField inserts a custom_field_definitions row and returns its
// id as the string key shape cfv maps use.
func insertCustomField(t *testing.T, tdb *testutils.TestDB, name, fieldType, options string) string {
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

// WI-319: text/textarea custom-field values must be bounded by the
// sanitize policies at the shared validation choke point.
func TestValidateAndNormalizeCustomFieldValues_SanitizesText(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	textKey := insertCustomField(t, tdb, "Short Text", "text", "")
	textareaKey := insertCustomField(t, tdb, "Long Text", "textarea", "")

	t.Run("text strips HTML and caps length", func(t *testing.T) {
		long := strings.Repeat("a", 400)
		cfv := map[string]interface{}{
			textKey: "<script>alert(1)</script>" + long,
		}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		got, ok := cfv[textKey].(string)
		if !ok {
			t.Fatalf("expected string value, got %T", cfv[textKey])
		}
		if strings.Contains(got, "<script>") {
			t.Errorf("HTML tag survived sanitization: %q", got)
		}
		if len([]rune(got)) > 256 {
			t.Errorf("text value not capped at 256 runes, got %d", len([]rune(got)))
		}
	})

	t.Run("textarea strips HTML and neutralizes dangerous markdown URLs", func(t *testing.T) {
		cfv := map[string]interface{}{
			textareaKey: "line one\nline two <img src=x onerror=alert(1)>\n[click](javascript:alert(1))",
		}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		got := cfv[textareaKey].(string)
		if strings.Contains(got, "<img") {
			t.Errorf("HTML tag survived sanitization: %q", got)
		}
		if strings.Contains(got, "javascript:") {
			t.Errorf("dangerous markdown URL survived sanitization: %q", got)
		}
		if !strings.Contains(got, "line one\nline two") {
			t.Errorf("newlines should be preserved, got: %q", got)
		}
	})

	t.Run("textarea keeps long content above the PlainTextField cap", func(t *testing.T) {
		long := strings.Repeat("b", 5000)
		cfv := map[string]interface{}{textareaKey: long}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if got := cfv[textareaKey].(string); len(got) != 5000 {
			t.Errorf("textarea content should not be truncated at the title cap, got len %d", len(got))
		}
	})

	t.Run("non-string text value passes through untouched", func(t *testing.T) {
		cfv := map[string]interface{}{textKey: float64(42)}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if cfv[textKey] != float64(42) {
			t.Errorf("non-string value should pass through, got %v", cfv[textKey])
		}
	})

	t.Run("unknown field keys are left untouched", func(t *testing.T) {
		cfv := map[string]interface{}{"99999": "<b>raw</b>"}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if cfv["99999"] != "<b>raw</b>" {
			t.Errorf("unknown-key value should be untouched, got %v", cfv["99999"])
		}
	})
}

func TestSanitizeCustomFieldTextValues(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	textKey := insertCustomField(t, tdb, "Form Text", "text", "")
	selectKey := insertCustomField(t, tdb, "Form Select", "select", `{"items":[{"id":1,"label":"One"}]}`)

	cfv := map[string]interface{}{
		textKey: "<script>x</script>hello",
		// An out-of-set select value: the sanitize-only pass must not
		// reject it — shape validation is the caller's concern.
		selectKey: float64(12345),
	}
	if err := SanitizeCustomFieldTextValues(db, cfv); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := cfv[textKey].(string); got != "hello" {
		t.Errorf("expected sanitized text %q, got %q", "hello", got)
	}
	if cfv[selectKey] != float64(12345) {
		t.Errorf("select value must pass through the sanitize-only pass, got %v", cfv[selectKey])
	}
}

// SanitizeCustomFieldTextValues sanitizes text fields for prevalidated writes.
func SanitizeCustomFieldTextValues(db database.Database, cfv map[string]any) error {
	if len(cfv) == 0 {
		return nil
	}
	fields, err := loadFieldsForCFV(db, cfv)
	if err != nil {
		return fmt.Errorf("load custom fields for sanitization: %w", err)
	}
	for fieldKey, raw := range cfv {
		def, ok := fields[fieldKey]
		if !ok {
			continue
		}
		switch def.FieldType {
		case "text", "textarea":
			cfv[fieldKey] = sanitizeTextValue(def.FieldType, raw)
		}
	}
	return nil
}

func TestValidateAndNormalizeCustomFieldValues_Milestones(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	multiKey := insertCustomField(t, tdb, "Affected versions", "multi_milestone", "")
	singleKey := insertCustomField(t, tdb, "Fix version", "milestone", "")

	first := insertMilestone(t, tdb, "1.0")
	second := insertMilestone(t, tdb, "2.0")

	t.Run("multi normalizes ids and keeps first-seen order", func(t *testing.T) {
		cfv := map[string]any{
			multiKey: []any{float64(first), strconv.Itoa(second), first},
		}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("ValidateAndNormalizeCustomFieldValues: %v", err)
		}
		want := []int{first, second}
		if !reflect.DeepEqual(cfv[multiKey], want) {
			t.Fatalf("normalized value = %#v, want %#v", cfv[multiKey], want)
		}
	})

	t.Run("multi drops blank entries and clears an empty selection", func(t *testing.T) {
		cfv := map[string]any{multiKey: []any{"", "  "}}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("ValidateAndNormalizeCustomFieldValues: %v", err)
		}
		if want := []int{}; !reflect.DeepEqual(cfv[multiKey], want) {
			t.Fatalf("empty multi selection = %#v, want %#v", cfv[multiKey], want)
		}
	})

	t.Run("multi rejects non-array values", func(t *testing.T) {
		cfv := map[string]any{multiKey: first}
		err := ValidateAndNormalizeCustomFieldValues(db, cfv)
		validationErr, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("error = %T %v, want *ValidationError", err, err)
		}
		if validationErr.Field != "custom_field_values."+multiKey {
			t.Fatalf("field = %q, want custom_field_values.%s", validationErr.Field, multiKey)
		}
		if !strings.Contains(validationErr.Message, "array") {
			t.Fatalf("message = %q, want it to mention an array", validationErr.Message)
		}
	})

	t.Run("multi rejects unknown milestone ids", func(t *testing.T) {
		cfv := map[string]any{multiKey: []any{first, 999999}}
		err := ValidateAndNormalizeCustomFieldValues(db, cfv)
		validationErr, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("error = %T %v, want *ValidationError", err, err)
		}
		if !strings.Contains(validationErr.Message, "999999 not found") {
			t.Fatalf("message = %q, want the missing milestone id", validationErr.Message)
		}
	})

	t.Run("single normalizes a numeric string", func(t *testing.T) {
		cfv := map[string]any{singleKey: strconv.Itoa(second)}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("ValidateAndNormalizeCustomFieldValues: %v", err)
		}
		if cfv[singleKey] != second {
			t.Fatalf("normalized value = %#v, want %d", cfv[singleKey], second)
		}
	})

	t.Run("single clears on blank", func(t *testing.T) {
		cfv := map[string]any{singleKey: "  "}
		if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
			t.Fatalf("ValidateAndNormalizeCustomFieldValues: %v", err)
		}
		if cfv[singleKey] != nil {
			t.Fatalf("blank single value = %#v, want nil", cfv[singleKey])
		}
	})

	t.Run("single rejects unknown milestone ids", func(t *testing.T) {
		cfv := map[string]any{singleKey: 999999}
		err := ValidateAndNormalizeCustomFieldValues(db, cfv)
		validationErr, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("error = %T %v, want *ValidationError", err, err)
		}
		if !strings.Contains(validationErr.Message, "999999 not found") {
			t.Fatalf("message = %q, want the missing milestone id", validationErr.Message)
		}
	})
}

func insertMilestone(t *testing.T, tdb *testutils.TestDB, name string) int {
	t.Helper()
	var id int
	err := tdb.QueryRow(`
		INSERT INTO milestones (name, status, is_global, position)
		VALUES (?, 'planning', TRUE, 0) RETURNING id
	`, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert milestone %q: %v", name, err)
	}
	return id
}

// WI-1428: option ids are integers, so an accepted select/multiselect value is
// rewritten to its canonical int id rather than left as the submitted JSON type.
func TestValidateAndNormalizeCustomFieldValues_NormalizesOptionIDs(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	selectKey := insertCustomField(t, tdb, "Priority", "select",
		`{"next_id":3,"items":[{"id":1,"label":"Low"},{"id":2,"label":"High"}]}`)
	multiKey := insertCustomField(t, tdb, "Tags", "multiselect",
		`{"next_id":3,"items":[{"id":1,"label":"A"},{"id":2,"label":"B"}]}`)

	tests := []struct {
		name string
		key  string
		raw  any
		want any
	}{
		{name: "select integral JSON number becomes int", key: selectKey, raw: float64(1), want: 1},
		{name: "select legacy numeric string becomes int", key: selectKey, raw: "2", want: 2},
		{name: "multiselect integral JSON numbers become ints", key: multiKey, raw: []any{float64(2), 1}, want: []int{2, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfv := map[string]any{tt.key: tt.raw}
			if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
				t.Fatalf("ValidateAndNormalizeCustomFieldValues: %v", err)
			}
			if !reflect.DeepEqual(cfv[tt.key], tt.want) {
				t.Fatalf("normalized value = %#v (%T), want %#v (%T)", cfv[tt.key], cfv[tt.key], tt.want, tt.want)
			}
		})
	}
}

// WI-1428: a fractional or non-finite JSON number must be rejected instead of
// being truncated into an existing option id.
func TestValidateAndNormalizeCustomFieldValues_RejectsNonIntegralOptionIDs(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	selectKey := insertCustomField(t, tdb, "Priority", "select",
		`{"next_id":3,"items":[{"id":1,"label":"Low"},{"id":2,"label":"High"}]}`)
	multiKey := insertCustomField(t, tdb, "Tags", "multiselect",
		`{"next_id":3,"items":[{"id":1,"label":"A"},{"id":2,"label":"B"}]}`)
	milestoneKey := insertCustomField(t, tdb, "Fix version", "milestone", "")
	first := insertMilestone(t, tdb, "1.0")

	tests := []struct {
		name        string
		key         string
		raw         any
		wantMessage string
	}{
		{name: "select fractional number", key: selectKey, raw: float64(1.9), wantMessage: "integer"},
		{name: "select NaN", key: selectKey, raw: math.NaN(), wantMessage: "integer"},
		{name: "select positive infinity", key: selectKey, raw: math.Inf(1), wantMessage: "integer"},
		{name: "select integral but unknown id", key: selectKey, raw: float64(99), wantMessage: "option set"},
		{name: "multiselect fractional number", key: multiKey, raw: []any{1, 2.5}, wantMessage: "integer"},
		{name: "milestone fractional number", key: milestoneKey, raw: float64(first) + 0.5, wantMessage: "integer"},
	}
	for _, tt := range tests {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			cfv := map[string]any{tt.key: tt.raw}
			err := ValidateAndNormalizeCustomFieldValues(db, cfv)
			if err == nil {
				t.Fatalf("value %#v (%T) was accepted", tt.raw, tt.raw)
			}
			validationErr, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("error = %T %v, want *ValidationError", err, err)
			}
			if validationErr.Field != "custom_field_values."+tt.key {
				t.Fatalf("field = %q, want custom_field_values.%s", validationErr.Field, tt.key)
			}
			if !strings.Contains(validationErr.Message, tt.wantMessage) {
				t.Fatalf("message = %q, want it to contain %q", validationErr.Message, tt.wantMessage)
			}
		})
	}
}
