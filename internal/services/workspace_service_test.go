//go:build test

package services_test

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

type allowTemplateSourceAccess struct{}

func (allowTemplateSourceAccess) CanViewWorkspaceTx(context.Context, database.Tx, int, int) (bool, error) {
	return true, nil
}

// workspaceTestEnv contains test data for workspace service tests
type workspaceTestEnv struct {
	WorkspaceID   int
	WorkspaceName string
	WorkspaceKey  string
	UserID        int
}

// createWorkspaceTestDB creates a test database for workspace service tests
func createWorkspaceTestDB(t *testing.T) database.Database {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	return tdb.GetDatabase()
}

// setupWorkspaceTestEnv creates test data for workspace service tests using the factory
func setupWorkspaceTestEnv(t *testing.T, db database.Database) workspaceTestEnv {
	t.Helper()
	f := factory.NewTestFactory(db)

	// Create user first
	userID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	// Create workspace with explicit values for testing
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{
		Name:        "Test Workspace",
		Key:         "WST",
		Description: "Test workspace",
		CreatorID:   userID,
	})
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	return workspaceTestEnv{
		WorkspaceID:   workspaceID,
		WorkspaceName: "Test Workspace",
		WorkspaceKey:  "WST",
		UserID:        userID,
	}
}

func TestWorkspaceService_List(t *testing.T) {
	db := createWorkspaceTestDB(t)

	service := services.NewWorkspaceService(db)
	env := setupWorkspaceTestEnv(t, db)

	t.Run("ReturnsAccessibleWorkspaces", func(t *testing.T) {
		workspaces, total, err := service.List(services.WorkspaceListParams{
			WorkspaceIDs: []int{env.WorkspaceID},
			Limit:        100,
			Offset:       0,
		})
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(workspaces) == 0 {
			t.Error("Expected at least one workspace")
		}
		if total == 0 {
			t.Error("Expected total to be at least 1")
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		workspaces, _, err := service.List(services.WorkspaceListParams{
			WorkspaceIDs: []int{env.WorkspaceID},
			Limit:        1,
			Offset:       0,
		})
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(workspaces) > 1 {
			t.Errorf("Expected at most 1 workspace with limit 1, got %d", len(workspaces))
		}
	})

	t.Run("EmptyAuthorizedScope", func(t *testing.T) {
		workspaces, total, err := service.List(services.WorkspaceListParams{
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("List empty authorized scope: %v", err)
		}
		if len(workspaces) != 0 || total != 0 {
			t.Fatalf("List empty authorized scope = %d workspaces, total %d; want 0, 0", len(workspaces), total)
		}
	})

	t.Run("LargeAuthorizedScopeUsesBulkFilter", func(t *testing.T) {
		workspaceIDs := make([]int, 1000)
		for i := range workspaceIDs {
			workspaceIDs[i] = env.WorkspaceID + i
		}
		workspaces, total, err := service.List(services.WorkspaceListParams{
			WorkspaceIDs: workspaceIDs,
			Limit:        100,
			Offset:       0,
		})
		if err != nil {
			t.Fatalf("List large authorized scope: %v", err)
		}
		if len(workspaces) != 1 || total != 1 || workspaces[0].ID != env.WorkspaceID {
			t.Fatalf("List large authorized scope = %+v, total %d; want workspace %d only", workspaces, total, env.WorkspaceID)
		}
	})
}

func TestWorkspaceService_GetByID(t *testing.T) {
	db := createWorkspaceTestDB(t)

	service := services.NewWorkspaceService(db)
	env := setupWorkspaceTestEnv(t, db)

	t.Run("Success", func(t *testing.T) {
		workspace, err := service.GetByID(env.WorkspaceID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if workspace.ID != env.WorkspaceID {
			t.Errorf("Expected workspace ID %d, got %d", env.WorkspaceID, workspace.ID)
		}
		if workspace.Name != env.WorkspaceName {
			t.Errorf("Expected workspace name '%s', got '%s'", env.WorkspaceName, workspace.Name)
		}
		if workspace.Key != env.WorkspaceKey {
			t.Errorf("Expected workspace key '%s', got '%s'", env.WorkspaceKey, workspace.Key)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := service.GetByID(99999)
		if err == nil {
			t.Error("Expected error for non-existent workspace")
		}
	})
}

func TestWorkspaceService_Create(t *testing.T) {
	db := createWorkspaceTestDB(t)

	service := services.NewWorkspaceService(db)
	env := setupWorkspaceTestEnv(t, db)

	t.Run("Success", func(t *testing.T) {
		params := services.CreateWorkspaceParams{
			Name:        "New Workspace",
			Key:         "new",
			Description: "A new workspace",
			CreatorID:   env.UserID,
		}

		result, err := service.Create(t.Context(), params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if result.Workspace.Name != "New Workspace" {
			t.Errorf("Expected name 'New Workspace', got '%s'", result.Workspace.Name)
		}
		// Key should be uppercased
		if result.Workspace.Key != "NEW" {
			t.Errorf("Expected key 'NEW', got '%s'", result.Workspace.Key)
		}
	})

	t.Run("PersistsTimeProject", func(t *testing.T) {
		var timeProjectID int
		if err := db.QueryRow(`
			INSERT INTO time_projects (name)
			VALUES ('Workspace default project')
			RETURNING id
		`).Scan(&timeProjectID); err != nil {
			t.Fatalf("Create time project: %v", err)
		}

		result, err := service.Create(t.Context(), services.CreateWorkspaceParams{
			Name:          "Workspace With Time Project",
			Key:           "WTP",
			Description:   "Uses a default time project",
			CreatorID:     env.UserID,
			TimeProjectID: &timeProjectID,
		})
		if err != nil {
			t.Fatalf("Create workspace: %v", err)
		}
		if result.Workspace.TimeProjectID == nil || *result.Workspace.TimeProjectID != timeProjectID {
			t.Fatalf("Created workspace time project = %v, want %d", result.Workspace.TimeProjectID, timeProjectID)
		}

		workspace, err := service.GetByID(result.Workspace.ID)
		if err != nil {
			t.Fatalf("Reload workspace: %v", err)
		}
		if workspace.TimeProjectID == nil || *workspace.TimeProjectID != timeProjectID {
			t.Fatalf("Reloaded workspace time project = %v, want %d", workspace.TimeProjectID, timeProjectID)
		}
	})

	t.Run("DuplicateKey", func(t *testing.T) {
		params := services.CreateWorkspaceParams{
			Name:        "Duplicate Workspace",
			Key:         env.WorkspaceKey, // Same as existing
			Description: "Should fail",
			CreatorID:   env.UserID,
		}

		_, err := service.Create(t.Context(), params)
		if !errors.Is(err, repository.ErrDuplicateEntry) {
			t.Fatalf("Create error = %v, want ErrDuplicateEntry", err)
		}
	})
}

func TestWorkspaceService_CreateFromTemplateHandlesNullableCanonicalBooleans(t *testing.T) {
	t.Run("nullable template flag is ineligible", func(t *testing.T) {
		db := createWorkspaceTestDB(t)
		env := setupWorkspaceTestEnv(t, db)

		// is_template is nullable in the production schema, but the API never
		// creates NULL. Set it directly to exercise that valid persisted shape.
		if _, err := db.Exec("UPDATE workspaces SET is_template = NULL WHERE id = ?", env.WorkspaceID); err != nil {
			t.Fatalf("Set nullable template flag: %v", err)
		}

		service := services.NewWorkspaceServiceWithAccess(db, allowTemplateSourceAccess{})
		_, err := service.Create(t.Context(), services.CreateWorkspaceParams{
			Name:                "Clone of nullable template",
			Key:                 "CNT",
			CreatorID:           env.UserID,
			TemplateWorkspaceID: &env.WorkspaceID,
		})
		if !errors.Is(err, services.ErrInvalidWorkspaceTemplate) {
			t.Fatalf("Create error = %v, want ErrInvalidWorkspaceTemplate", err)
		}

		var destinations int
		if err := db.QueryRow("SELECT COUNT(*) FROM workspaces WHERE key = 'CNT'").Scan(&destinations); err != nil {
			t.Fatalf("Count rolled-back destination: %v", err)
		}
		if destinations != 0 {
			t.Fatalf("Destination rows = %d, want 0", destinations)
		}
	})

	t.Run("nullable seed task flag clones as false", func(t *testing.T) {
		db := createWorkspaceTestDB(t)
		env := setupWorkspaceTestEnv(t, db)
		itemFactory := factory.NewTestFactory(db)
		itemID, err := itemFactory.CreateItem(factory.CreateItemOpts{
			WorkspaceID: env.WorkspaceID,
			Title:       "Legacy nullable task flag",
			CreatorID:   &env.UserID,
		})
		if err != nil {
			t.Fatalf("Create source item: %v", err)
		}

		// is_task remains nullable for compatibility with older rows. The
		// production API always writes a bool, so this compatibility shape
		// must be installed directly.
		if _, err := db.Exec("UPDATE workspaces SET is_template = true WHERE id = ?", env.WorkspaceID); err != nil {
			t.Fatalf("Mark source as template: %v", err)
		}
		if _, err := db.Exec("UPDATE items SET is_task = NULL WHERE id = ?", itemID); err != nil {
			t.Fatalf("Set nullable task flag: %v", err)
		}

		service := services.NewWorkspaceServiceWithAccess(db, allowTemplateSourceAccess{})
		result, err := service.Create(t.Context(), services.CreateWorkspaceParams{
			Name:                "Clone of nullable seed",
			Key:                 "CNS",
			CreatorID:           env.UserID,
			TemplateWorkspaceID: &env.WorkspaceID,
		})
		if err != nil {
			t.Fatalf("Create from template: %v", err)
		}
		if result.ItemsCopied != 1 {
			t.Fatalf("ItemsCopied = %d, want 1", result.ItemsCopied)
		}

		var isTask bool
		if err := db.QueryRow("SELECT is_task FROM items WHERE workspace_id = ?", result.Workspace.ID).Scan(&isTask); err != nil {
			t.Fatalf("Load cloned task flag: %v", err)
		}
		if isTask {
			t.Fatal("Cloned nullable task flag = true, want false")
		}
	})
}

func TestWorkspaceService_Update(t *testing.T) {
	db := createWorkspaceTestDB(t)

	service := services.NewWorkspaceService(db)
	env := setupWorkspaceTestEnv(t, db)

	t.Run("Success", func(t *testing.T) {
		newName := "Updated Workspace"
		newDesc := "Updated description"
		params := services.UpdateWorkspaceParams{
			ID:          env.WorkspaceID,
			Name:        &newName,
			Description: &newDesc,
		}

		workspace, err := service.Update(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if workspace.Name != "Updated Workspace" {
			t.Errorf("Expected name 'Updated Workspace', got '%s'", workspace.Name)
		}
		if workspace.Description != "Updated description" {
			t.Errorf("Expected description 'Updated description', got '%s'", workspace.Description)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		newName := "Non-existent"
		params := services.UpdateWorkspaceParams{
			ID:   99999,
			Name: &newName,
		}

		_, err := service.Update(params)
		if err == nil {
			t.Error("Expected error for non-existent workspace")
		}
	})
}

func TestWorkspaceService_Delete(t *testing.T) {
	db := createWorkspaceTestDB(t)

	service := services.NewWorkspaceService(db)
	env := setupWorkspaceTestEnv(t, db)

	t.Run("Success", func(t *testing.T) {
		// Create a workspace to delete
		result, _ := service.Create(t.Context(), services.CreateWorkspaceParams{
			Name:        "To Delete",
			Key:         "DEL",
			Description: "Will be deleted",
			CreatorID:   env.UserID,
		})

		err := service.Delete(result.Workspace.ID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify deletion
		_, err = service.GetByID(result.Workspace.ID)
		if err == nil {
			t.Error("Expected error for deleted workspace")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		err := service.Delete(99999)
		if err == nil {
			t.Error("Expected error for non-existent workspace")
		}
	})
}

func TestWorkspaceService_Exists(t *testing.T) {
	db := createWorkspaceTestDB(t)

	service := services.NewWorkspaceService(db)
	env := setupWorkspaceTestEnv(t, db)

	t.Run("ExistingWorkspace", func(t *testing.T) {
		exists, err := service.Exists(env.WorkspaceID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if !exists {
			t.Error("Expected workspace to exist")
		}
	})

	t.Run("NonExistentWorkspace", func(t *testing.T) {
		exists, err := service.Exists(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if exists {
			t.Error("Expected workspace to not exist")
		}
	})
}

func TestWorkspaceService_KeyExists(t *testing.T) {
	db := createWorkspaceTestDB(t)

	service := services.NewWorkspaceService(db)
	env := setupWorkspaceTestEnv(t, db)

	t.Run("ExistingKey", func(t *testing.T) {
		exists, err := service.KeyExists(env.WorkspaceKey)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if !exists {
			t.Error("Expected key to exist")
		}
	})

	t.Run("ExistingKeyLowercase", func(t *testing.T) {
		// Should work case-insensitively (key is normalized to uppercase)
		exists, err := service.KeyExists("wst")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if !exists {
			t.Error("Expected key to exist (case-insensitive)")
		}
	})

	t.Run("NonExistentKey", func(t *testing.T) {
		exists, err := service.KeyExists("NONEXISTENT")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if exists {
			t.Error("Expected key to not exist")
		}
	})
}
