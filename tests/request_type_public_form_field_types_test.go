package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// WI-1427: request-type field management must apply the same public-form
// fillability filter as runtime serving and submission validation. A custom
// field type the public form cannot render (user, asset, milestone, ...) must
// be rejected on save instead of persisting as an invisible required field.
func TestRequestTypeFieldsRejectNonPublicFormCustomFieldTypes(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Public Form Field Types WS", shortKey("PFFT"))
	_, channelID := SetupPortalChannel(t, server, wsID)

	configSetID := GetDefaultConfigurationSet(t, server)
	AssociateWorkspaceWithConfigSet(t, server, wsID, configSetID)
	taskID := RequireItemTypeID(t, GetWorkspaceItemTypes(t, server, wsID), "Task")

	createResp := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/channels/%d/request-types", channelID), map[string]interface{}{
			"name":         "Public form field types",
			"item_type_id": taskID,
			"workspace_id": wsID,
			"is_active":    true,
		})
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	createResp.Body.Close()
	requestTypeID := intField(created, "id")

	userFieldID := CreateTestCustomField(t, server, "Public form assignee", "user", "")
	textFieldID := CreateTestCustomField(t, server, "Public form summary", "text", "")
	AddCustomFieldsToCreateScreens(t, server, wsID, []int{taskID}, []int{userFieldID, textFieldID})

	fieldsPath := fmt.Sprintf("/channels/%d/request-types/%d/fields", channelID, requestTypeID)
	getFieldsPath := fmt.Sprintf("/request-types/%d/fields", requestTypeID)

	nonFillable := []map[string]interface{}{
		{"field_identifier": "title", "field_type": "default", "display_order": 0, "is_required": true},
		{"field_identifier": fmt.Sprintf("%d", userFieldID), "field_type": "custom", "display_order": 1, "is_required": true},
	}
	resp := MakeAuthRequest(t, server, http.MethodPut, fieldsPath, nonFillable)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "is not available on the target create screen") {
		t.Fatalf("non-fillable field save: status=%d body=%s, want 400 unavailable-field", resp.StatusCode, body)
	}

	getResp := MakeAuthRequest(t, server, http.MethodGet, getFieldsPath, nil)
	AssertStatusCode(t, getResp, http.StatusOK)
	var persisted []map[string]interface{}
	DecodeJSON(t, getResp, &persisted)
	getResp.Body.Close()
	if len(persisted) != 0 {
		t.Fatalf("rejected save persisted %d fields: %+v", len(persisted), persisted)
	}

	// Positive control: a public-form-fillable field still saves.
	fillable := []map[string]interface{}{
		{"field_identifier": "title", "field_type": "default", "display_order": 0, "is_required": true},
		{"field_identifier": fmt.Sprintf("%d", textFieldID), "field_type": "custom", "display_order": 1, "is_required": true},
	}
	saveResp := MakeAuthRequest(t, server, http.MethodPut, fieldsPath, fillable)
	AssertStatusCode(t, saveResp, http.StatusOK)
	saveResp.Body.Close()

	getResp = MakeAuthRequest(t, server, http.MethodGet, getFieldsPath, nil)
	AssertStatusCode(t, getResp, http.StatusOK)
	persisted = nil
	DecodeJSON(t, getResp, &persisted)
	getResp.Body.Close()
	if len(persisted) != 2 {
		t.Fatalf("fillable save persisted %d fields, want 2: %+v", len(persisted), persisted)
	}
}
