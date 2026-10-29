//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/jira"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// WI-891: the importer-boundary converter accepts the Jira boolean shapes seen
// in fixtures (raw bool and single-element wrapper arrays/objects) and never
// coerces unknown shapes into truthy data.
func TestJiraCheckboxValueConversion(t *testing.T) {
	tests := []struct {
		name   string
		raw    interface{}
		want   bool
		wantOK bool
	}{
		{name: "raw true", raw: true, want: true, wantOK: true},
		{name: "raw false", raw: false, want: false, wantOK: true},
		{name: "single-element array true", raw: []interface{}{true}, want: true, wantOK: true},
		{name: "single-element array false", raw: []interface{}{false}, want: false, wantOK: true},
		{name: "wrapper object value true", raw: map[string]interface{}{"value": true}, want: true, wantOK: true},
		{name: "exact string true", raw: "true", want: true, wantOK: true},
		{name: "exact string false", raw: "false", want: false, wantOK: true},
		{name: "wrapper exact string", raw: map[string]interface{}{"value": "true"}, want: true, wantOK: true},
		{name: "two-element array not coerced", raw: []interface{}{true, false}, wantOK: false},
		{name: "null not coerced", raw: nil, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := jiraCheckboxValue(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("value = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJiraAssetBooleanAttributeValue(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  jira.AssetAttributeValue
		want bool
	}{
		{name: "raw boolean", raw: jira.AssetAttributeValue{Value: false}, want: false},
		{name: "display string", raw: jira.AssetAttributeValue{DisplayValue: "true"}, want: true},
		{name: "search string", raw: jira.AssetAttributeValue{SearchValue: "false"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := jiraAssetBooleanAttributeValue(jira.AssetObjectAttributeValue{
				ObjectAttributeValues: []jira.AssetAttributeValue{test.raw},
			})
			if !ok || got != test.want {
				t.Fatalf("got (%v, %v), want (%v, true)", got, ok, test.want)
			}
		})
	}

	if got := jiraAssetAttributeFieldType(jira.AssetObjectAttribute{Type: 0, DefaultTypeID: 2}); got != models.CustomFieldTypeBoolean {
		t.Fatalf("asset boolean field type = %q, want %q", got, models.CustomFieldTypeBoolean)
	}
}

func TestInferFieldTypeRecognizesExplicitBooleans(t *testing.T) {
	if fieldType, options := services.InferAssetImportFieldType([]string{"true", "FALSE", "true"}); fieldType != models.CustomFieldTypeBoolean || len(options) != 0 {
		t.Fatalf("inferFieldType() = (%q, %#v), want (%q, nil)", fieldType, options, models.CustomFieldTypeBoolean)
	}
	if fieldType, _ := services.InferAssetImportFieldType([]string{"true", "unknown"}); fieldType == models.CustomFieldTypeBoolean {
		t.Fatal("mixed values must not be inferred as boolean")
	}
}

// WI-891: checkbox custom-field definitions accept absent/empty options,
// reject non-empty options, and canonicalize the compatibility alias.
func TestCustomFieldHandler_CheckboxDefinition(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	handler := createCustomFieldHandler(t, tdb)

	t.Run("create checkbox without options succeeds", func(t *testing.T) {
		cf := createField(t, handler, "Approved", "checkbox")
		if cf.FieldType != models.CustomFieldTypeBoolean {
			t.Errorf("expected canonical field_type boolean, got %q", cf.FieldType)
		}
		if cf.Options != "" {
			t.Errorf("expected empty options, got %q", cf.Options)
		}
	})

	t.Run("create checkbox with empty options payload succeeds", func(t *testing.T) {
		body := map[string]interface{}{
			"name":       "Verified",
			"field_type": "checkbox",
			"options":    "",
		}
		req := testutils.CreateJSONRequest(t, "POST", "/api/custom-fields", body)
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
		rr.AssertStatusCode(http.StatusCreated)
	})

	t.Run("create checkbox with non-empty options is rejected with 400", func(t *testing.T) {
		body := map[string]interface{}{
			"name":       "Bad Options",
			"field_type": "checkbox",
			"options":    `{"next_id":1,"items":[{"id":1,"label":"Yes"}]}`,
		}
		req := testutils.CreateJSONRequest(t, "POST", "/api/custom-fields", body)
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
		rr.AssertStatusCode(http.StatusBadRequest)
		rr.AssertBodyContains("Checkbox fields do not support options")
	})

	t.Run("update checkbox through compatibility alias", func(t *testing.T) {
		cf := createField(t, handler, "Environment", "checkbox")
		update := map[string]interface{}{
			"name":       "Environment approval",
			"field_type": "checkbox",
		}
		req := testutils.CreateJSONRequest(t, "PUT", "/api/custom-fields/"+testutils.IntToString(cf.ID), update)
		req.SetPathValue("id", testutils.IntToString(cf.ID))
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
		rr.AssertStatusCode(http.StatusOK)

		var updated models.CustomFieldDefinition
		rr.AssertJSONResponse(&updated)
		if updated.FieldType != models.CustomFieldTypeBoolean {
			t.Errorf("expected canonical field_type boolean, got %q", updated.FieldType)
		}
		if updated.Options != "" {
			t.Errorf("expected empty options, got %q", updated.Options)
		}
	})
}
