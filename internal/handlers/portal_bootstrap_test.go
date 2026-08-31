package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/database"
	"windshift/internal/middleware"
	"windshift/internal/models"
)

func setupPortalBootstrapTest(t *testing.T) (database.Database, int, int) {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "portal-bootstrap.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var workspaceID, itemTypeID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Portal bootstrap', 'PB') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Portal bootstrap request') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	config := fmt.Sprintf(`{"portal_slug":"support","portal_title":"Support","portal_workspace_ids":[%d]}`, workspaceID)
	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES ('Support portal', 'portal', 'inbound', 'enabled', ?, 'support') RETURNING id
	`, config).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active)
		VALUES (?, 'Ask support', ?, ?, true)
	`, channelID, itemTypeID, workspaceID); err != nil {
		t.Fatalf("insert request type: %v", err)
	}
	return db, channelID, workspaceID
}

func authenticatedPortalBootstrapRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/portal/support/bootstrap", nil)
	request.SetPathValue("slug", "support")
	return request.WithContext(context.WithValue(request.Context(), middleware.ContextKeySession, &auth.Session{UserID: 1}))
}

func TestAuthenticatedPortalBootstrapComposesShellCatalogs(t *testing.T) {
	db, channelID, _ := setupPortalBootstrapTest(t)
	handler := NewPortalHandler(db, nil, nil, nil, "")
	request := authenticatedPortalBootstrapRequest()
	recorder := httptest.NewRecorder()

	handler.GetBootstrap(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response PortalBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Portal["channel_id"] != float64(channelID) {
		t.Fatalf("portal = %+v, want channel %d", response.Portal, channelID)
	}
	if len(response.RequestTypes) != 1 || response.RequestTypes[0].Name != "Ask support" {
		t.Fatalf("request types = %+v, want Ask support", response.RequestTypes)
	}
	if response.AssetReports == nil {
		t.Fatal("asset_reports must encode as an empty array, not null")
	}
}

func TestAuthenticatedPortalBootstrapAssetReportsUseExactSafeContract(t *testing.T) {
	db, channelID, workspaceID := setupPortalBootstrapTest(t)
	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types WHERE name = 'Portal bootstrap request'`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load item type: %v", err)
	}
	var assetSetID int
	if err := db.QueryRow(`INSERT INTO asset_management_sets (name) VALUES ('Internal inventory') RETURNING id`).Scan(&assetSetID); err != nil {
		t.Fatalf("insert asset set: %v", err)
	}
	var reportID int
	if err := db.QueryRow(`
		INSERT INTO asset_reports
			(channel_id, asset_set_id, name, description, cql_query, icon, color, display_order,
			 column_config, visibility_group_ids, visibility_org_ids, run_mode, item_type_id, workspace_id, config)
		VALUES (?, ?, 'Guest inventory', 'Safe presentation', 'secret_internal = true', 'Table2', '#123456', 7,
		        '["title"]', NULL, NULL, 'form', ?, ?,
		        '{"require_auth":true,"success_message":"Safe success","submit_button_text":"Run safely","redirect_url":"https://internal.example.test"}')
		RETURNING id
	`, channelID, assetSetID, itemTypeID, workspaceID).Scan(&reportID); err != nil {
		t.Fatalf("insert asset report: %v", err)
	}

	handler := NewPortalHandler(db, nil, nil, nil, "")
	request := authenticatedPortalBootstrapRequest()
	recorder := httptest.NewRecorder()
	handler.GetBootstrap(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response PortalBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got, err := json.Marshal(response.AssetReports)
	if err != nil {
		t.Fatalf("marshal asset reports: %v", err)
	}
	want := fmt.Sprintf(`[{"id":%d,"name":"Guest inventory","description":"Safe presentation","icon":"Table2","color":"#123456","display_order":7,"column_config":["title"],"run_mode":"form","item_type_id":%d,"workspace_id":%d,"config":{"success_message":"Safe success","submit_button_text":"Run safely"}}]`, reportID, itemTypeID, workspaceID)
	if string(got) != want {
		t.Fatalf("asset_reports contract = %s, want %s", got, want)
	}
}

func TestPortalUserBootstrapUsesValidatedInternalContext(t *testing.T) {
	db, _, _ := setupPortalBootstrapTest(t)
	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('portal-user@example.test', 'portal-user', 'Portal', 'User', true) RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	user := &models.User{ID: userID, Email: "portal-user@example.test", FirstName: "Portal", LastName: "User"}
	session := &auth.Session{UserID: userID, User: user}

	handler := NewPortalHandler(db, nil, nil, nil, "")
	request := httptest.NewRequest(http.MethodGet, "/api/portal/support/user-bootstrap", nil)
	request.SetPathValue("slug", "support")
	request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeySession, session))
	recorder := httptest.NewRecorder()

	handler.GetUserBootstrap(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response PortalUserBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Authenticated || !response.IsInternal || response.User["id"] != float64(userID) {
		t.Fatalf("auth response = %+v, want internal user %d", response, userID)
	}
	if response.MyRequests == nil || response.MyApprovals == nil {
		t.Fatal("badge datasets must encode as empty arrays, not null")
	}
}

func TestPortalUserBootstrapReturnsAnonymousSnapshotWithoutSession(t *testing.T) {
	db, _, _ := setupPortalBootstrapTest(t)
	handler := NewPortalHandler(db, nil, nil, nil, "")
	request := httptest.NewRequest(http.MethodGet, "/api/portal/support/user-bootstrap", nil)
	request.SetPathValue("slug", "support")
	recorder := httptest.NewRecorder()

	handler.GetUserBootstrap(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response PortalUserBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Authenticated {
		t.Fatalf("authenticated = true, want false: %+v", response)
	}
	if response.MyRequests == nil || response.MyApprovals == nil {
		t.Fatal("anonymous badge datasets must encode as empty arrays, not null")
	}
}
