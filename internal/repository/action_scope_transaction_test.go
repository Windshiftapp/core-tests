package repository

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

func newActionScopeTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "action-scope.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	if err := db.Initialize(); err != nil {
		_ = db.Close()
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertActionScopeWorkspace(t *testing.T, db database.Database) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Action scope', 'ACT', '', true, false)
		RETURNING id
	`).Scan(&id); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	return id
}

func tableRowCount(t *testing.T, db database.Database, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestCreateCapabilityWithWorkspacesRollsBackCapability(t *testing.T) {
	db := newActionScopeTestDB(t)
	repo := NewActionRepository(db)
	capability := &models.ActionCapability{
		Name:                   "scoped HTTP",
		CapabilityType:         models.CapabilityHTTPClient,
		Config:                 `{"allowed_url_patterns":["https://example.com/**"]}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: false,
	}

	if _, err := repo.CreateCapabilityWithWorkspaces(capability, []int{999999}); err == nil {
		t.Fatal("create with nonexistent workspace unexpectedly succeeded")
	}
	if got := tableRowCount(t, db, "action_capabilities"); got != 0 {
		t.Fatalf("action_capabilities count = %d after rollback, want 0", got)
	}
}

func TestUpdateCapabilityWithWorkspacesRollsBackMetadataAndScope(t *testing.T) {
	db := newActionScopeTestDB(t)
	workspaceID := insertActionScopeWorkspace(t, db)
	repo := NewActionRepository(db)
	capability := &models.ActionCapability{
		Name:                   "original",
		CapabilityType:         models.CapabilityHTTPClient,
		Config:                 `{"allowed_url_patterns":["https://example.com/**"]}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: false,
	}
	if _, err := repo.CreateCapabilityWithWorkspaces(capability, []int{workspaceID}); err != nil {
		t.Fatalf("create capability fixture: %v", err)
	}

	capability.Name = "should roll back"
	if err := repo.UpdateCapabilityWithWorkspaces(capability, []int{999999}); err == nil {
		t.Fatal("update with nonexistent workspace unexpectedly succeeded")
	}
	stored, err := repo.GetCapabilityByID(capability.ID)
	if err != nil {
		t.Fatalf("GetCapabilityByID: %v", err)
	}
	if stored.Name != "original" {
		t.Fatalf("stored name = %q after rollback, want original", stored.Name)
	}
	if len(stored.WorkspaceIDs) != 1 || stored.WorkspaceIDs[0] != workspaceID {
		t.Fatalf("stored workspace IDs = %v after rollback, want [%d]", stored.WorkspaceIDs, workspaceID)
	}
}

func TestCreateActionCredentialWithWorkspacesRollsBackCredential(t *testing.T) {
	db := newActionScopeTestDB(t)
	repo := NewActionCredentialRepository(db)
	credential := &models.ActionCredential{
		Name:                   "scoped secret",
		CredentialType:         models.CredentialAPIKey,
		AppliesToAllWorkspaces: false,
		EncryptedSecret:        "ciphertext",
		IsEnabled:              true,
	}

	if _, err := repo.CreateActionCredentialWithWorkspaces(credential, []int{999999}); err == nil {
		t.Fatal("create with nonexistent workspace unexpectedly succeeded")
	}
	if got := tableRowCount(t, db, "action_credentials"); got != 0 {
		t.Fatalf("action_credentials count = %d after rollback, want 0", got)
	}
}

func TestUpdateActionCredentialWithWorkspacesRollsBackMetadataAndScope(t *testing.T) {
	db := newActionScopeTestDB(t)
	workspaceID := insertActionScopeWorkspace(t, db)
	repo := NewActionCredentialRepository(db)
	credential := &models.ActionCredential{
		Name:                   "original",
		CredentialType:         models.CredentialAPIKey,
		AppliesToAllWorkspaces: false,
		EncryptedSecret:        "ciphertext",
		IsEnabled:              true,
	}
	if _, err := repo.CreateActionCredentialWithWorkspaces(credential, []int{workspaceID}); err != nil {
		t.Fatalf("create credential fixture: %v", err)
	}

	credential.Name = "should roll back"
	if err := repo.UpdateActionCredentialMetadataWithWorkspaces(credential, []int{999999}); err == nil {
		t.Fatal("update with nonexistent workspace unexpectedly succeeded")
	}
	stored, err := repo.GetActionCredentialByID(credential.ID)
	if err != nil {
		t.Fatalf("GetActionCredentialByID: %v", err)
	}
	if stored.Name != "original" {
		t.Fatalf("stored name = %q after rollback, want original", stored.Name)
	}
	if len(stored.WorkspaceIDs) != 1 || stored.WorkspaceIDs[0] != workspaceID {
		t.Fatalf("stored workspace IDs = %v after rollback, want [%d]", stored.WorkspaceIDs, workspaceID)
	}
}
