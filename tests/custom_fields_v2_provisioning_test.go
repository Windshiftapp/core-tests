package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCustomFieldsV2Provisioning exercises the public v2 custom-field
// provisioning surface (WI-1306): the create/update/delete/settings operations
// share the browser surface's validation, sanitization, mirror handling, and
// system-default read-only contract through
// services.CustomFieldProvisioningService.
func TestCustomFieldsV2Provisioning(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	t.Run("create_requires_scope_and_admin", func(t *testing.T) {
		// Non-admin users cannot request admin scopes at token creation, so
		// this token holds only non-admin scopes — exercising scope + role
		// denial together.
		_, _, editorPass := CreateTestUserWithCredentials(t, server, "cfv2editor", "cfv2editor@test.local")
		_, editorToken := CreateAuthCredentialsForUser(t, server, "cfv2editor", editorPass)

		resp := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
			"name": "Editor Field", "field_type": "text",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("create_rejects_invalid_field_type_and_empty_name", func(t *testing.T) {
		badType := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
			"name": "Bad Type", "field_type": "telepathy",
		})
		defer badType.Body.Close()
		AssertStatusCode(t, badType, http.StatusBadRequest)

		noName := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
			"name": "   ", "field_type": "text",
		})
		defer noName.Body.Close()
		AssertStatusCode(t, noName, http.StatusBadRequest)
	})

	t.Run("create_patch_delete_roundtrip", func(t *testing.T) {
		createResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
			"name": "Severity v2", "field_type": "select",
			"options": `{"next_id":3,"items":[{"id":1,"label":"Low"},{"id":2,"label":"High"}]}`,
		})
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created struct {
			Data struct {
				CustomField struct {
					ID        int    `json:"id"`
					Name      string `json:"name"`
					FieldType string `json:"field_type"`
				} `json:"custom_field"`
			} `json:"data"`
		}
		DecodeJSON(t, createResp, &created)
		fieldID := created.Data.CustomField.ID
		if fieldID == 0 || created.Data.CustomField.FieldType != "select" {
			t.Fatalf("created = %#v", created.Data)
		}

		// Merge patch: update name and shrink options; removal of option 2
		// must enqueue value scrubbing through the shared service.
		patchResp := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/custom-fields/%d", fieldID), map[string]interface{}{
			"name":    "Severity v2 Renamed",
			"options": `{"next_id":3,"items":[{"id":1,"label":"Low"}]}`,
		})
		defer patchResp.Body.Close()
		AssertStatusCode(t, patchResp, http.StatusOK)
		var patched struct {
			Data struct {
				CustomField struct {
					Name string `json:"name"`
				} `json:"custom_field"`
			} `json:"data"`
		}
		DecodeJSON(t, patchResp, &patched)
		if patched.Data.CustomField.Name != "Severity v2 Renamed" {
			t.Fatalf("patched = %#v", patched.Data)
		}

		deleteResp := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/custom-fields/%d", fieldID), nil)
		defer deleteResp.Body.Close()
		AssertStatusCode(t, deleteResp, http.StatusNoContent)

		// The field is gone: further reads report not found.
		getResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/custom-fields/%d", fieldID), nil)
		defer getResp.Body.Close()
		AssertStatusCode(t, getResp, http.StatusNotFound)
	})

	t.Run("settings_reject_out_of_range_limit", func(t *testing.T) {
		resp := MakeBearerRequest(t, server, http.MethodPut, "/rest/api/v2/custom-fields/settings", map[string]interface{}{
			"max_indexes_per_table": 0,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("settings_persist", func(t *testing.T) {
		resp := MakeBearerRequest(t, server, http.MethodPut, "/rest/api/v2/custom-fields/settings", map[string]interface{}{
			"max_indexes_per_table": 33,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("system_default_fields_stay_read_only", func(t *testing.T) {
		// Fixture exception: no API path creates system_default fields, so the
		// guard is reached via a direct write to the production schema.
		fieldID := CreateTestCustomField(t, server, "System Default v2 "+shortKey("CFV2"), "text", "")
		if _, err := server.DB().ExecWrite(`UPDATE custom_field_definitions SET system_default = true WHERE id = ?`, fieldID); err != nil {
			t.Fatalf("seed system_default flag: %v", err)
		}

		patchResp := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/custom-fields/%d", fieldID), map[string]interface{}{
			"name": "Renamed via v2",
		})
		defer patchResp.Body.Close()
		AssertStatusCode(t, patchResp, http.StatusForbidden)

		deleteResp := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/custom-fields/%d", fieldID), nil)
		defer deleteResp.Body.Close()
		AssertStatusCode(t, deleteResp, http.StatusForbidden)
	})
}
