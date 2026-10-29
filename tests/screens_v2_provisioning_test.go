package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestScreensV2Provisioning exercises the public v2 screen surface (WI-1306):
// reads, admin-gated writes, always-visible system field enforcement, default
// screen deletion guard, and the non-admin denial contract.
func TestScreensV2Provisioning(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	t.Run("mutations_require_system_admin", func(t *testing.T) {
		// Non-admin users cannot request admin scopes at token creation, so
		// this token holds only non-admin scopes — exercising scope + role
		// denial together.
		_, _, editorPass := CreateTestUserWithCredentials(t, server, "scrv2editor", "scrv2editor@test.local")
		_, editorToken := CreateAuthCredentialsForUser(t, server, "scrv2editor", editorPass)

		resp := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost, "/rest/api/v2/screens", map[string]interface{}{
			"name": "Editor Screen",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("create_rejects_missing_name", func(t *testing.T) {
		resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/screens", map[string]interface{}{
			"name": "  ",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("create_get_delete_roundtrip", func(t *testing.T) {
		createResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/screens", map[string]interface{}{
			"name": "Provisioned Screen", "description": "from v2",
		})
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created struct {
			Data struct {
				ID    int    `json:"id"`
				Name  string `json:"name"`
				Fields []struct {
					FieldIdentifier string `json:"field_identifier"`
					FieldType       string `json:"field_type"`
				} `json:"fields"`
			} `json:"data"`
		}
		DecodeJSON(t, createResp, &created)
		screenID := created.Data.ID
		if screenID == 0 {
			t.Fatalf("created = %#v", created.Data)
		}

		getResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/screens/%d", screenID), nil)
		defer getResp.Body.Close()
		AssertStatusCode(t, getResp, http.StatusOK)

		deleteResp := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/screens/%d", screenID), nil)
		defer deleteResp.Body.Close()
		AssertStatusCode(t, deleteResp, http.StatusNoContent)
	})

	t.Run("builtin_screens_cannot_be_deleted", func(t *testing.T) {
		listResp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/screens", nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)
		var list struct {
			Data []struct {
				ID         int    `json:"id"`
				BuiltinKey string `json:"builtin_key"`
			} `json:"data"`
		}
		DecodeJSON(t, listResp, &list)

		builtinIDs := map[string]int{}
		for _, screen := range list.Data {
			if screen.BuiltinKey != "" {
				builtinIDs[screen.BuiltinKey] = screen.ID
			}
		}
		// Both the full edit screen and the lean create screen are built-in.
		for _, key := range []string{"default", "default_create"} {
			id, ok := builtinIDs[key]
			if !ok {
				t.Fatalf("builtin screen %q not found in %#v", key, list.Data)
			}
			resp := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/screens/%d", id), nil)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusBadRequest)
		}
	})

	t.Run("replace_fields_enforces_always_visible_system_fields", func(t *testing.T) {
		createResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/screens", map[string]interface{}{
			"name": "Field Replace Screen",
		})
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created struct {
			Data struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		DecodeJSON(t, createResp, &created)
		screenID := created.Data.ID

		// Replace with one real custom field; title/description/status must be
		// re-added with their fixed required/width settings.
		fieldID := CreateTestCustomField(t, server, "Screen v2 Field "+shortKey("SCRF"), "text", "")
		putResp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/screens/%d/fields", screenID), map[string]interface{}{
			"fields": []map[string]interface{}{
				{"field_type": "custom", "field_identifier": fmt.Sprintf("%d", fieldID), "display_order": 0, "is_required": false, "field_width": "full"},
			},
		})
		defer putResp.Body.Close()
		AssertStatusCode(t, putResp, http.StatusOK)
		var replaced struct {
			Data []struct {
				FieldType       string `json:"field_type"`
				FieldIdentifier string `json:"field_identifier"`
				DisplayOrder    int    `json:"display_order"`
				IsRequired      bool   `json:"is_required"`
				FieldWidth      string `json:"field_width"`
			} `json:"data"`
		}
		DecodeJSON(t, putResp, &replaced)
		if len(replaced.Data) != 4 {
			t.Fatalf("replaced fields = %#v, want custom field plus three always-visible system fields", replaced.Data)
		}
		identifiers := map[string]bool{}
		for _, field := range replaced.Data {
			identifiers[field.FieldIdentifier] = true
		}
		if !identifiers[fmt.Sprintf("%d", fieldID)] {
			t.Errorf("custom field %d missing after replace: %#v", fieldID, replaced.Data)
		}
		for _, required := range []string{"title", "description", "status"} {
			if !identifiers[required] {
				t.Errorf("always-visible field %q missing after replace: %#v", required, replaced.Data)
			}
		}

		// Cleanup
		del := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/screens/%d", screenID), nil)
		_ = del.Body.Close()
		AssertStatusCode(t, del, http.StatusNoContent)
	})
}
