//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

const validWidgetProgram = `root = Card(children=[Title(text="Open work"), ItemsTable(query="status_completed = false")])`

func newCustomWidgetEnv(t *testing.T) (*CustomWidgetHandler, *testutils.TestDB) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	permissionService, _, _ := createTestServices(t, *tdb)
	handler := NewCustomWidgetHandler(repository.NewCustomWidgetRepository(tdb.GetDatabase()), permissionService)
	return handler, tdb
}

func insertWidgetUser(t *testing.T, tdb *testutils.TestDB, email, username string) int {
	t.Helper()
	var userID int
	if err := tdb.GetDatabase().QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES (?, ?, 'Widget', 'User') RETURNING id
	`, email, username).Scan(&userID); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return userID
}

// seedWidgetWorkspace creates an active workspace and grants one workspace
// role. Unlike seedWorkspaceWithRole it does not add the phantom admin user,
// which needs a pre-seeded user row this test does not create.
func seedWidgetWorkspace(t *testing.T, tdb *testutils.TestDB, workspaceID, userID int, role string) {
	t.Helper()
	db := tdb.GetDatabase()
	if _, err := db.ExecWrite(`INSERT INTO workspaces (id, name, key, active) VALUES (?, 'Widget WS', ?, TRUE) ON CONFLICT (id) DO NOTHING`, workspaceID, "WWS"+strconv.Itoa(workspaceID)); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	var roleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, role).Scan(&roleID); err != nil {
		t.Fatalf("look up role %s: %v", role, err)
	}
	if _, err := db.ExecWrite(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, userID, workspaceID, roleID); err != nil {
		t.Fatalf("assign workspace role: %v", err)
	}
}

func callCustomWidgets(t *testing.T, handler *CustomWidgetHandler, userID int, method, path, pathID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(context.WithValue(req.Context(), contextkeys.User, &models.User{ID: userID}))
	if pathID != "" {
		req.SetPathValue("id", pathID)
	}
	rr := httptest.NewRecorder()

	switch {
	case method == http.MethodGet && pathID == "":
		handler.GetCustomWidgets(rr, req)
	case method == http.MethodGet:
		handler.GetCustomWidget(rr, req)
	case method == http.MethodPost && path == "/api/custom-widgets/validate":
		handler.ValidateCustomWidget(rr, req)
	case method == http.MethodPost:
		handler.CreateCustomWidget(rr, req)
	case method == http.MethodPut:
		handler.UpdateCustomWidget(rr, req)
	case method == http.MethodDelete:
		handler.DeleteCustomWidget(rr, req)
	default:
		t.Fatalf("unsupported method %s", method)
	}
	return rr
}

func createWidget(t *testing.T, handler *CustomWidgetHandler, userID int, body map[string]any) int {
	t.Helper()
	rr := callCustomWidgets(t, handler, userID, http.MethodPost, "/api/custom-widgets", "", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var widget models.CustomWidget
	if err := json.Unmarshal(rr.Body.Bytes(), &widget); err != nil {
		t.Fatalf("decode created widget: %v", err)
	}
	return widget.ID
}

func widgetBody() map[string]any {
	return map[string]any{
		"name":            "My open work",
		"description":     "Items I still owe",
		"program":         validWidgetProgram,
		"lang_version":    "0.5",
		"library_version": "1",
		"queries":         map[string]any{"item": map[string]any{"ql": "status_completed = false"}},
	}
}

func TestCustomWidgetCRUDAndOwnership(t *testing.T) {
	handler, tdb := newCustomWidgetEnv(t)
	ownerID := insertWidgetUser(t, tdb, "widget-owner@example.test", "widget-owner")
	otherID := insertWidgetUser(t, tdb, "widget-other@example.test", "widget-other")

	var widgetID int

	t.Run("create defaults to a private widget owned by the caller", func(t *testing.T) {
		rr := callCustomWidgets(t, handler, ownerID, http.MethodPost, "/api/custom-widgets", "", widgetBody())
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
		}
		var widget models.CustomWidget
		if err := json.Unmarshal(rr.Body.Bytes(), &widget); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if widget.OwnerUserID == nil || *widget.OwnerUserID != ownerID {
			t.Fatalf("owner_user_id = %v, want %d", widget.OwnerUserID, ownerID)
		}
		if widget.WorkspaceID != nil {
			t.Fatalf("workspace_id = %v, want nil", widget.WorkspaceID)
		}
		if widget.Visibility != models.CustomWidgetVisibilityPrivate {
			t.Fatalf("visibility = %q, want private", widget.Visibility)
		}
		if string(widget.Queries) == "" || string(widget.Queries) == "null" {
			t.Fatalf("queries = %s, want normalized JSON", widget.Queries)
		}
		widgetID = widget.ID
	})

	t.Run("owner can read the widget", func(t *testing.T) {
		rr := callCustomWidgets(t, handler, ownerID, http.MethodGet, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("another user cannot read, update, or delete it", func(t *testing.T) {
		get := callCustomWidgets(t, handler, otherID, http.MethodGet, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if get.Code != http.StatusNotFound {
			t.Fatalf("get status = %d, want 404", get.Code)
		}
		update := callCustomWidgets(t, handler, otherID, http.MethodPut, "/api/custom-widgets", strconv.Itoa(widgetID), widgetBody())
		if update.Code != http.StatusNotFound {
			t.Fatalf("update status = %d, want 404", update.Code)
		}
		del := callCustomWidgets(t, handler, otherID, http.MethodDelete, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if del.Code != http.StatusNotFound {
			t.Fatalf("delete status = %d, want 404", del.Code)
		}
	})

	t.Run("owner update persists the new program", func(t *testing.T) {
		body := widgetBody()
		body["program"] = `root = Text(text="changed")`
		rr := callCustomWidgets(t, handler, ownerID, http.MethodPut, "/api/custom-widgets", strconv.Itoa(widgetID), body)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		var widget models.CustomWidget
		if err := json.Unmarshal(rr.Body.Bytes(), &widget); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if widget.Program != `root = Text(text="changed")` {
			t.Fatalf("program = %q, want the updated program", widget.Program)
		}
	})

	t.Run("list returns only the caller's widgets", func(t *testing.T) {
		ownerList := callCustomWidgets(t, handler, ownerID, http.MethodGet, "/api/custom-widgets", "", nil)
		if ownerList.Code != http.StatusOK {
			t.Fatalf("owner list status = %d", ownerList.Code)
		}
		var ownerWidgets []models.CustomWidget
		if err := json.Unmarshal(ownerList.Body.Bytes(), &ownerWidgets); err != nil {
			t.Fatalf("decode owner list: %v", err)
		}
		if len(ownerWidgets) != 1 || ownerWidgets[0].ID != widgetID {
			t.Fatalf("owner list = %+v, want just widget %d", ownerWidgets, widgetID)
		}

		otherList := callCustomWidgets(t, handler, otherID, http.MethodGet, "/api/custom-widgets", "", nil)
		var otherWidgets []models.CustomWidget
		if err := json.Unmarshal(otherList.Body.Bytes(), &otherWidgets); err != nil {
			t.Fatalf("decode other list: %v", err)
		}
		if len(otherWidgets) != 0 {
			t.Fatalf("other list = %+v, want empty", otherWidgets)
		}
	})

	t.Run("owner delete removes the widget", func(t *testing.T) {
		del := callCustomWidgets(t, handler, ownerID, http.MethodDelete, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if del.Code != http.StatusNoContent {
			t.Fatalf("delete status = %d, want 204; body=%s", del.Code, del.Body.String())
		}
		get := callCustomWidgets(t, handler, ownerID, http.MethodGet, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if get.Code != http.StatusNotFound {
			t.Fatalf("get after delete status = %d, want 404", get.Code)
		}
	})
}

func TestCustomWidgetValidation(t *testing.T) {
	handler, tdb := newCustomWidgetEnv(t)
	userID := insertWidgetUser(t, tdb, "widget-validation@example.test", "widget-validation")

	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "blank name", body: func() map[string]any { b := widgetBody(); b["name"] = "  "; return b }()},
		{name: "unknown component", body: func() map[string]any { b := widgetBody(); b["program"] = `root = Wibble()`; return b }()},
		{name: "program without root", body: func() map[string]any { b := widgetBody(); b["program"] = `chart = BarChart()`; return b }()},
		{
			name: "unparseable cql",
			body: func() map[string]any {
				b := widgetBody()
				b["queries"] = map[string]any{"item": map[string]any{"ql": "status = "}}
				return b
			}(),
		},
		{
			name: "asset slot without a set",
			body: func() map[string]any {
				b := widgetBody()
				b["queries"] = map[string]any{"asset": map[string]any{"ql": ""}}
				return b
			}(),
		},
		{
			name: "unknown query field",
			body: func() map[string]any {
				b := widgetBody()
				b["queries"] = map[string]any{"items": map[string]any{"ql": ""}}
				return b
			}(),
		},
	}

	for _, test := range cases {
		t.Run("rejects "+test.name, func(t *testing.T) {
			rr := callCustomWidgets(t, handler, userID, http.MethodPost, "/api/custom-widgets", "", test.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	t.Run("nothing was persisted", func(t *testing.T) {
		list := callCustomWidgets(t, handler, userID, http.MethodGet, "/api/custom-widgets", "", nil)
		var widgets []models.CustomWidget
		if err := json.Unmarshal(list.Body.Bytes(), &widgets); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(widgets) != 0 {
			t.Fatalf("list = %+v, want empty", widgets)
		}
	})

	t.Run("validate endpoint accepts a valid payload", func(t *testing.T) {
		rr := callCustomWidgets(t, handler, userID, http.MethodPost, "/api/custom-widgets/validate", "", widgetBody())
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("validate endpoint rejects an invalid payload", func(t *testing.T) {
		body := widgetBody()
		body["program"] = `root = Wibble()`
		rr := callCustomWidgets(t, handler, userID, http.MethodPost, "/api/custom-widgets/validate", "", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
		}
	})
}

func TestCustomWidgetWorkspacePermissions(t *testing.T) {
	handler, tdb := newCustomWidgetEnv(t)
	editorID := insertWidgetUser(t, tdb, "widget-editor@example.test", "widget-editor")
	viewerID := insertWidgetUser(t, tdb, "widget-viewer@example.test", "widget-viewer")
	outsiderID := insertWidgetUser(t, tdb, "widget-outsider@example.test", "widget-outsider")

	const workspaceID = 4242
	seedWidgetWorkspace(t, tdb, workspaceID, editorID, "Editor")
	seedWidgetWorkspace(t, tdb, workspaceID, viewerID, "Viewer")

	body := widgetBody()
	body["workspace_id"] = workspaceID
	body["visibility"] = models.CustomWidgetVisibilityWorkspace
	widgetID := createWidget(t, handler, editorID, body)

	t.Run("editor creates a workspace-visible widget", func(t *testing.T) {
		rr := callCustomWidgets(t, handler, editorID, http.MethodGet, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var widget models.CustomWidget
		if err := json.Unmarshal(rr.Body.Bytes(), &widget); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if widget.WorkspaceID == nil || *widget.WorkspaceID != workspaceID {
			t.Fatalf("workspace_id = %v, want %d", widget.WorkspaceID, workspaceID)
		}
		if widget.OwnerUserID != nil {
			t.Fatalf("owner_user_id = %v, want nil for a workspace widget", widget.OwnerUserID)
		}
		if widget.Visibility != models.CustomWidgetVisibilityWorkspace {
			t.Fatalf("visibility = %q, want workspace", widget.Visibility)
		}
	})

	t.Run("a viewer can read but not modify it", func(t *testing.T) {
		get := callCustomWidgets(t, handler, viewerID, http.MethodGet, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if get.Code != http.StatusOK {
			t.Fatalf("get status = %d, want 200", get.Code)
		}
		update := callCustomWidgets(t, handler, viewerID, http.MethodPut, "/api/custom-widgets", strconv.Itoa(widgetID), body)
		if update.Code != http.StatusForbidden {
			t.Fatalf("update status = %d, want 403", update.Code)
		}
		del := callCustomWidgets(t, handler, viewerID, http.MethodDelete, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if del.Code != http.StatusForbidden {
			t.Fatalf("delete status = %d, want 403", del.Code)
		}
	})

	t.Run("a user outside the workspace cannot see it", func(t *testing.T) {
		get := callCustomWidgets(t, handler, outsiderID, http.MethodGet, "/api/custom-widgets", strconv.Itoa(widgetID), nil)
		if get.Code != http.StatusNotFound {
			t.Fatalf("get status = %d, want 404", get.Code)
		}

		list := callCustomWidgets(t, handler, outsiderID, http.MethodGet, "/api/custom-widgets", "", nil)
		var widgets []models.CustomWidget
		if err := json.Unmarshal(list.Body.Bytes(), &widgets); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		for _, widget := range widgets {
			if widget.ID == widgetID {
				t.Fatalf("outsider list leaked workspace widget %d", widgetID)
			}
		}
	})

	t.Run("a user outside the workspace cannot create in it", func(t *testing.T) {
		rr := callCustomWidgets(t, handler, outsiderID, http.MethodPost, "/api/custom-widgets", "", body)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
		}
	})
}

func TestDashboardLayoutAcceptsCustomWidgetType(t *testing.T) {
	handler, userID := newDashboardLayoutHandler(t)

	layout := models.UserDashboardLayout{
		Sections: []models.UserDashboardSection{{
			ID:           "s-1",
			Title:        "Custom",
			DisplayOrder: 0,
			WidgetIDs:    []string{"w-1"},
		}},
		Widgets: []models.UserDashboardWidget{{
			ID:        "w-1",
			Type:      "custom-widget",
			SectionID: "s-1",
			Position:  0,
			Width:     6,
			Config:    map[string]any{"customWidgetId": 12},
		}},
	}

	rr := putLayout(t, handler, userID, layout)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var saved models.UserDashboardLayout
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(saved.Widgets) != 1 || saved.Widgets[0].Type != "custom-widget" {
		t.Fatalf("saved widgets = %+v, want the custom-widget type preserved", saved.Widgets)
	}
}
