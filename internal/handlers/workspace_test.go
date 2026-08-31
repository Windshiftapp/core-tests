//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestWorkspaceHandler_Create_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	workspace := models.Workspace{
		Name:        "Test Workspace",
		Key:         "TEST",
		Description: "Test workspace for unit testing",
		Active:      true,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/workspaces", workspace)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusCreated).
		AssertContentType("application/json")

	var response models.Workspace
	rr.AssertJSONResponse(&response)

	if response.ID == 0 {
		t.Error("Expected created workspace to have an ID")
	}
	if response.Name != workspace.Name {
		t.Errorf("Expected name %s, got %s", workspace.Name, response.Name)
	}
	if response.Key != workspace.Key {
		t.Errorf("Expected key %s, got %s", workspace.Key, response.Key)
	}
	if response.Description != workspace.Description {
		t.Errorf("Expected description %s, got %s", workspace.Description, response.Description)
	}
	if response.Active != workspace.Active {
		t.Errorf("Expected active %v, got %v", workspace.Active, response.Active)
	}
	if response.CreatedAt.IsZero() {
		t.Error("Expected CreatedAt to be set")
	}
	if response.UpdatedAt.IsZero() {
		t.Error("Expected UpdatedAt to be set")
	}

	// Verify workspace was actually inserted into database
	var count int
	err := tdb.QueryRow("SELECT COUNT(*) FROM workspaces WHERE name = ?", workspace.Name).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to verify workspace creation: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 workspace in database, got %d", count)
	}
}

func TestWorkspaceHandler_Create_ValidationErrors(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	tests := []struct {
		name        string
		workspace   models.Workspace
		expectedErr string
	}{
		{
			name:        "Missing name",
			workspace:   models.Workspace{Key: "TEST", Description: "Test"},
			expectedErr: "Name is required",
		},
		{
			name:        "Empty name",
			workspace:   models.Workspace{Name: "   ", Key: "TEST", Description: "Test"},
			expectedErr: "Workspace name is required",
		},
		{
			name:        "Missing key",
			workspace:   models.Workspace{Name: "Test", Description: "Test"},
			expectedErr: "Key is required",
		},
		{
			name:        "Empty key",
			workspace:   models.Workspace{Name: "Test", Key: "   ", Description: "Test"},
			expectedErr: "Key must contain only alphanumeric characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, "POST", "/api/workspaces", tt.workspace)
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

			testutils.AssertValidationError(t, rr, tt.expectedErr)
		})
	}
}

func TestWorkspaceHandler_Get_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create test workspace
	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, created_at, updated_at)
		VALUES ('Test Workspace', 'TEST', 'Test workspace', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("Failed to create test workspace: %v", err)
	}

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/"+testutils.IntToString(int(workspaceID)), nil)
	req.SetPathValue("id", testutils.IntToString(int(workspaceID)))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response models.Workspace
	rr.AssertJSONResponse(&response)

	if response.ID != int(workspaceID) {
		t.Errorf("Expected ID %d, got %d", workspaceID, response.ID)
	}
	if response.Name != "Test Workspace" {
		t.Errorf("Expected name 'Test Workspace', got %s", response.Name)
	}
	if response.Key != "TEST" {
		t.Errorf("Expected key 'TEST', got %s", response.Key)
	}
	if response.Description != "Test workspace" {
		t.Errorf("Expected description 'Test workspace', got %s", response.Description)
	}
	if !response.Active {
		t.Error("Expected workspace to be active")
	}
}

func TestWorkspaceHandler_Get_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

func TestWorkspaceHandler_GetAll_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create multiple test workspaces
	workspaces := []struct {
		name string
		key  string
		desc string
	}{
		{"Workspace A", "WSA", "First workspace"},
		{"Workspace B", "WSB", "Second workspace"},
		{"Workspace C", "WSC", "Third workspace"},
	}

	for _, ws := range workspaces {
		_, err := tdb.Exec(`
			INSERT INTO workspaces (name, key, description, active, created_at, updated_at)
			VALUES (?, ?, ?, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, ws.name, ws.key, ws.desc)
		if err != nil {
			t.Fatalf("Failed to create workspace %s: %v", ws.name, err)
		}
	}

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response []models.Workspace
	rr.AssertJSONResponse(&response)

	if len(response) != len(workspaces) {
		t.Errorf("Expected %d workspaces, got %d", len(workspaces), len(response))
	}

	// Verify workspaces are ordered by name
	expectedOrder := []string{"Workspace A", "Workspace B", "Workspace C"}
	for i, ws := range response {
		if ws.Name != expectedOrder[i] {
			t.Errorf("Expected workspace at position %d to be %s, got %s", i, expectedOrder[i], ws.Name)
		}
	}
}

func TestWorkspaceHandler_Update_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create test workspace
	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (
			name, key, description, active, is_personal, owner_id, icon, color,
			avatar_url, default_view, internal_comments_enabled, created_at, updated_at
		)
		VALUES (
			'Original Name', 'ORIG', 'Original description', true, true, 1, 'Package', '#123456',
			'/api/attachments/42/download', 'list', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("Failed to create test workspace: %v", err)
	}

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	const updatedName = "Updated Name"
	const updatedDescription = "Updated description"
	const updatedActive = false
	update := map[string]any{
		"name":        updatedName,
		"description": updatedDescription,
		"active":      updatedActive,
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/workspaces/"+testutils.IntToString(int(workspaceID)), update)
	req.SetPathValue("id", testutils.IntToString(int(workspaceID)))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response models.Workspace
	rr.AssertJSONResponse(&response)

	if response.Name != updatedName {
		t.Errorf("Expected name %s, got %s", updatedName, response.Name)
	}
	if response.Description != updatedDescription {
		t.Errorf("Expected description %s, got %s", updatedDescription, response.Description)
	}
	if response.Active != updatedActive {
		t.Errorf("Expected active %v, got %v", updatedActive, response.Active)
	}
	if response.Key != "ORIG" {
		t.Errorf("Expected key to remain unchanged as 'ORIG', got %s", response.Key)
	}
	if response.Icon != "Package" || response.Color != "#123456" {
		t.Errorf("Expected visual identity to be preserved, got icon=%q color=%q", response.Icon, response.Color)
	}
	if response.AvatarURL == nil || *response.AvatarURL != "/api/attachments/42/download" {
		t.Errorf("Expected avatar to be preserved, got %v", response.AvatarURL)
	}
	if !response.IsPersonal || response.OwnerID == nil || *response.OwnerID != 1 {
		t.Errorf("Expected personal workspace ownership to be preserved, got is_personal=%v owner_id=%v", response.IsPersonal, response.OwnerID)
	}
	if response.DefaultView != "list" || !response.InternalCommentsEnabled {
		t.Errorf("Expected workspace settings to be preserved, got default_view=%q internal_comments_enabled=%v", response.DefaultView, response.InternalCommentsEnabled)
	}

	// Verify database was updated
	var name, description string
	var active bool
	if err := tdb.QueryRow("SELECT name, description, active FROM workspaces WHERE id = ?", workspaceID).Scan(&name, &description, &active); err != nil {
		t.Fatalf("Failed to verify workspace update: %v", err)
	}
	if name != updatedName || description != updatedDescription || active != updatedActive {
		t.Error("Database was not updated correctly")
	}
}

func TestWorkspaceHandler_Update_ClearsExplicitNullAvatar(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, avatar_url, created_at, updated_at)
		VALUES ('Avatar Workspace', 'AVTR', '', true, '/api/attachments/42/download', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/workspaces/"+testutils.IntToString(workspaceID), map[string]any{
		"avatar_url": nil,
	})
	req.SetPathValue("id", testutils.IntToString(workspaceID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusOK)
	var response models.Workspace
	rr.AssertJSONResponse(&response)
	if response.AvatarURL != nil {
		t.Fatalf("Expected avatar to be cleared, got %q", *response.AvatarURL)
	}

	var cleared int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM workspaces WHERE id = ? AND avatar_url IS NULL", workspaceID).Scan(&cleared); err != nil {
		t.Fatalf("verify cleared avatar: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("Expected avatar_url to be NULL")
	}
}

func TestWorkspaceHandler_Delete_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create test workspace
	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, created_at, updated_at)
		VALUES ('Delete Me', 'DEL', 'To be deleted', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("Failed to create test workspace: %v", err)
	}

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/workspaces/"+testutils.IntToString(int(workspaceID)), nil)
	req.SetPathValue("id", testutils.IntToString(int(workspaceID)))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusNoContent)

	// Verify workspace was deleted
	var count int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM workspaces WHERE id = ?", workspaceID).Scan(&count); err != nil {
		t.Fatalf("Failed to verify workspace deletion: %v", err)
	}
	if count != 0 {
		t.Error("Workspace was not deleted from database")
	}
}

func TestWorkspaceHandler_InvalidID_Scenarios(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	tests := []struct {
		name     string
		endpoint string
		method   string
	}{
		{"Update invalid ID", "/api/workspaces/invalid", "PUT"},
		{"Delete invalid ID", "/api/workspaces/invalid", "DELETE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			switch tt.method {
			case "GET":
				req = testutils.CreateJSONRequest(t, tt.method, tt.endpoint, nil)
			case "PUT":
				req = testutils.CreateJSONRequest(t, tt.method, tt.endpoint, models.Workspace{Name: "Test"})
			case "DELETE":
				req = testutils.CreateJSONRequest(t, tt.method, tt.endpoint, nil)
			}

			// Set invalid ID in mux vars
			req.SetPathValue("id", "invalid")

			var rr *testutils.ResponseRecorder
			switch tt.method {
			case "GET":
				rr = testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)
			case "PUT":
				rr = testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
			case "DELETE":
				rr = testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)
			}

			// Non-numeric, non-matching path params resolve to "not found" via
			// the workspace key cache to avoid leaking existence.
			rr.AssertStatusCode(http.StatusNotFound)
		})
	}
}

func TestWorkspaceHandler_Get_ByKey(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create test workspace
	_, err := tdb.Exec(`
		INSERT INTO workspaces (name, key, description, active, created_at, updated_at)
		VALUES ('Test Workspace', 'TEST', 'Test workspace', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("Failed to create test workspace: %v", err)
	}

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/TEST", nil)
	req.SetPathValue("id", "TEST")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response models.Workspace
	rr.AssertJSONResponse(&response)

	if response.Name != "Test Workspace" {
		t.Errorf("Expected name 'Test Workspace', got %s", response.Name)
	}
	if response.Key != "TEST" {
		t.Errorf("Expected key 'TEST', got %s", response.Key)
	}
}

func TestWorkspaceHandler_Get_ByKey_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/NONEXISTENT", nil)
	req.SetPathValue("id", "NONEXISTENT")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

func TestWorkspaceHandler_DuplicateKey_Error(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create first workspace
	_, err := tdb.Exec(`
		INSERT INTO workspaces (name, key, description, active, created_at, updated_at)
		VALUES ('First Workspace', 'DUPLICATE', 'First workspace', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("Failed to create first workspace: %v", err)
	}

	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	handler := NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)

	// Try to create workspace with duplicate key
	duplicateWorkspace := models.Workspace{
		Name:        "Second Workspace",
		Key:         "DUPLICATE",
		Description: "Should fail due to duplicate key",
		Active:      true,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/workspaces", duplicateWorkspace)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	// Duplicate workspace key now returns 409 Conflict (was 500 under older repo).
	rr.AssertStatusCode(http.StatusConflict)
}
