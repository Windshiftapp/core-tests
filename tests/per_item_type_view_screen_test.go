package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestPerItemTypeViewScreenDetailSummary reproduces GH #261: a configuration
// set with per-item-type screen overrides ("Configure per item type") must
// surface the overridden view screen — including its configured system fields
// like estimate — on the item detail summary's screen context, not the
// config-set default screen.
func TestPerItemTypeViewScreenDetailSummary(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Per-Type Screen WS", "")

	// "Project Brief" screen configured with estimate among its system fields.
	createScreenResp := func(name string) int {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/screens", map[string]interface{}{
			"name": name,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var screen struct {
			ID int `json:"id"`
		}
		DecodeJSON(t, resp, &screen)
		if screen.ID == 0 {
			t.Fatalf("screen %q created without id: %#v", name, screen)
		}
		return screen.ID
	}
	briefID := createScreenResp("Project Brief")
	briefViewID := createScreenResp("Project Brief View")

	configureSystemFields := func(screenID int) {
		resp := MakeAuthRequest(t, server, http.MethodPut,
			fmt.Sprintf("/screens/%d/system-fields", screenID),
			[]string{
				"title", "description", "status", "priority",
				"assignee", "estimate", "story_points", "due_date",
			})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	}
	configureSystemFields(briefID)
	configureSystemFields(briefViewID)

	// Initiative's seeded item type id.
	typesResp := MakeAuthRequest(t, server, http.MethodGet, "/item-types", nil)
	defer typesResp.Body.Close()
	AssertStatusCode(t, typesResp, http.StatusOK)
	var itemTypes []map[string]interface{}
	DecodeJSON(t, typesResp, &itemTypes)
	var initiativeTypeID int
	for _, itemType := range itemTypes {
		if name, _ := itemType["name"].(string); name == "Initiative" {
			initiativeTypeID = ExtractIDFromResponse(t, itemType)
			break
		}
	}
	if initiativeTypeID == 0 {
		t.Fatal("seeded item types do not contain Initiative")
	}

	// Default configuration set, assigned to the workspace, with a per-type
	// override that points the Initiative create/edit screens at the Project
	// Brief and its view screen at a distinct per-type view screen. This
	// exercises both the itcCreate fallback and the itcView override.
	setResp := MakeAuthRequest(t, server, http.MethodPost, "/configuration-sets", map[string]interface{}{
		"name":          "Per-Type Screen Set",
		"workspace_ids": []int{wsID},
		"item_type_configs": []map[string]interface{}{
			{
				"item_type_id":     initiativeTypeID,
				"create_screen_id": briefID,
				"edit_screen_id":   briefID,
				"view_screen_id":   briefViewID,
			},
		},
	})
	defer setResp.Body.Close()
	AssertStatusCode(t, setResp, http.StatusCreated)

	// An Initiative item in that workspace.
	itemResp := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
		"title":        "Per-type screen initiative",
		"workspace_id": wsID,
		"item_type_id": initiativeTypeID,
	})
	defer itemResp.Body.Close()
	AssertStatusCode(t, itemResp, http.StatusCreated)
	var createdItem struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, itemResp, &createdItem)
	if createdItem.ID == 0 {
		t.Fatalf("item created without id: %#v", createdItem)
	}

	// The detail summary must resolve the per-type screens.
	summaryResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/items/%d/detail-summary", createdItem.ID), nil)
	defer summaryResp.Body.Close()
	AssertStatusCode(t, summaryResp, http.StatusOK)

	var summary struct {
		ScreenContext struct {
			Edit *struct {
				ID           int      `json:"id"`
				SystemFields []string `json:"system_fields"`
			} `json:"edit"`
			View *struct {
				ID           int      `json:"id"`
				SystemFields []string `json:"system_fields"`
			} `json:"view"`
		} `json:"screen_context"`
	}
	DecodeJSON(t, summaryResp, &summary)

	if summary.ScreenContext.Edit == nil || summary.ScreenContext.Edit.ID != briefID {
		t.Fatalf("screen_context.edit = %#v, want screen %d", summary.ScreenContext.Edit, briefID)
	}
	if summary.ScreenContext.View == nil || summary.ScreenContext.View.ID != briefViewID {
		t.Fatalf("screen_context.view = %#v, want screen %d", summary.ScreenContext.View, briefViewID)
	}

	// estimate drives the reported symptom: the sidebar renders it only when
	// the resolved screen configures it.
	hasEstimate := func(screen *struct {
		ID           int      `json:"id"`
		SystemFields []string `json:"system_fields"`
	}) bool {
		for _, field := range screen.SystemFields {
			if field == "estimate" || field == "estimate_minutes" {
				return true
			}
		}
		return false
	}
	if !hasEstimate(summary.ScreenContext.Edit) {
		t.Fatalf("screen_context.edit.system_fields = %#v, want estimate configured",
			summary.ScreenContext.Edit.SystemFields)
	}
	if !hasEstimate(summary.ScreenContext.View) {
		t.Fatalf("screen_context.view.system_fields = %#v, want estimate configured",
			summary.ScreenContext.View.SystemFields)
	}
}
