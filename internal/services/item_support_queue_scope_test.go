//go:build test

package services

import (
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// TestCreateQueuePersistsCollectionAndWorkspaceScope pins the scope storage
// contract for saved views: a queue created for a workspace-owned collection
// carries both ids, while a queue on a global collection keeps collection_id
// and leaves workspace_id unset.
func TestCreateQueuePersistsCollectionAndWorkspaceScope(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	permissions, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	creation := NewItemCreationService(tdb.GetDatabase(), permissions)
	update := NewItemUpdateApplicationService(tdb.GetDatabase(), permissions)
	deletion := NewItemDeletionApplicationService(tdb.GetDatabase(), permissions)
	application := NewItemApplicationService(tdb.GetDatabase(), permissions, creation, update, deletion)
	actor := AuditActor{UserID: data.UserID, Username: "testuser"}
	repo := repository.NewQueueRepository(tdb.GetDatabase())

	workspaceCollectionID := testutils.InsertID(t, tdb.GetDatabase(),
		`INSERT INTO collections (name, ql_query, workspace_id, created_by) VALUES ('Queue scoped collection', '', ?, ?)`,
		data.WorkspaceID, data.UserID)

	workspaceView, err := application.CreateQueue(t.Context(), actor, 0, &workspaceCollectionID, QueueInput{
		Name: "Workspace collection view",
		QL:   "statusCompleted = false",
	})
	if err != nil {
		t.Fatalf("CreateQueue workspace collection: %v", err)
	}
	workspaceRow, err := repo.GetByID(workspaceView.ID)
	if err != nil {
		t.Fatalf("GetByID workspace collection queue: %v", err)
	}
	if workspaceRow.WorkspaceID != data.WorkspaceID {
		t.Fatalf("workspace collection queue workspace_id = %d, want %d", workspaceRow.WorkspaceID, data.WorkspaceID)
	}
	if workspaceRow.CollectionID == nil || *workspaceRow.CollectionID != workspaceCollectionID {
		t.Fatalf("workspace collection queue collection_id = %v, want %d", workspaceRow.CollectionID, workspaceCollectionID)
	}

	globalCollectionID := testutils.InsertID(t, tdb.GetDatabase(),
		`INSERT INTO collections (name, ql_query, workspace_id, created_by) VALUES ('Global queue collection', '', NULL, ?)`,
		data.UserID)
	globalView, err := application.CreateQueue(t.Context(), actor, 0, &globalCollectionID, QueueInput{
		Name: "Global collection view",
		QL:   "statusCompleted = false",
	})
	if err != nil {
		t.Fatalf("CreateQueue global collection: %v", err)
	}
	globalRow, err := repo.GetByID(globalView.ID)
	if err != nil {
		t.Fatalf("GetByID global collection queue: %v", err)
	}
	if globalRow.WorkspaceID != 0 {
		t.Fatalf("global collection queue workspace_id = %d, want 0", globalRow.WorkspaceID)
	}
	if globalRow.CollectionID == nil || *globalRow.CollectionID != globalCollectionID {
		t.Fatalf("global collection queue collection_id = %v, want %d", globalRow.CollectionID, globalCollectionID)
	}
}
