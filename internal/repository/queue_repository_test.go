//go:build test

package repository

import (
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// queueFixture builds one workspace with a collection and a second workspace
// so scope isolation can be asserted without cross-test coupling.
type queueFixture struct {
	workspaceA   int
	workspaceB   int
	collectionID int
	userID       int
}

func setupQueueFixture(t *testing.T) (*QueueRepository, queueFixture) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	fixture := queueFixture{
		workspaceA:   testutils.InsertID(t, db, `INSERT INTO workspaces (name, key) VALUES ('Queue WS A', 'QWA')`),
		workspaceB:   testutils.InsertID(t, db, `INSERT INTO workspaces (name, key) VALUES ('Queue WS B', 'QWB')`),
		userID:       testutils.InsertID(t, db, `INSERT INTO users (email, username, first_name, last_name) VALUES ('queue-owner@example.test', 'queue-owner', 'Queue', 'Owner')`),
	}
	fixture.collectionID = testutils.InsertID(t, db,
		`INSERT INTO collections (name, ql_query, workspace_id) VALUES ('Queue collection', '', ?)`, fixture.workspaceA)

	return NewQueueRepository(db), fixture
}

func TestQueueRepositoryCreateAndGetRoundTrip(t *testing.T) {
	repo, fixture := setupQueueFixture(t)
	filterState := `{"statuses":[1]}`
	created, err := repo.Create(&models.Queue{
		WorkspaceID:  fixture.workspaceA,
		CollectionID: &fixture.collectionID,
		Name:         "My queue",
		QLQuery:      `assignee = currentUser()`,
		FilterState:  &filterState,
		Position:     3,
		CreatedBy:    &fixture.userID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID || got.WorkspaceID != fixture.workspaceA ||
		got.CollectionID == nil || *got.CollectionID != fixture.collectionID ||
		got.Name != "My queue" || got.QLQuery != `assignee = currentUser()` ||
		got.Position != 3 || got.IsHidden {
		t.Fatalf("round-trip queue = %+v", got)
	}
	if got.FilterState == nil || *got.FilterState != filterState {
		t.Fatalf("filter_state = %v, want %q", got.FilterState, filterState)
	}
	if got.CreatedBy == nil || *got.CreatedBy != fixture.userID {
		t.Fatalf("created_by = %v, want %d", got.CreatedBy, fixture.userID)
	}
	if got.BuiltinKey != nil {
		t.Fatalf("custom queue builtin_key = %v, want nil", got.BuiltinKey)
	}
}

func TestQueueRepositoryListByScopeIsolatesScopes(t *testing.T) {
	repo, fixture := setupQueueFixture(t)

	workspaceQueue, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceA, Name: "Workspace queue", QLQuery: "statusCompleted = false",
	})
	if err != nil {
		t.Fatalf("create workspace queue: %v", err)
	}
	collectionQueue, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceA, CollectionID: &fixture.collectionID,
		Name: "Collection queue", QLQuery: "assignee IS NULL",
	})
	if err != nil {
		t.Fatalf("create collection queue: %v", err)
	}
	otherWorkspaceQueue, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceB, Name: "Other workspace queue", QLQuery: "team IS NULL",
	})
	if err != nil {
		t.Fatalf("create other-workspace queue: %v", err)
	}

	workspaceRows, err := repo.ListByScope(fixture.workspaceA, nil)
	if err != nil {
		t.Fatalf("ListByScope workspace: %v", err)
	}
	if len(workspaceRows) != 1 || workspaceRows[0].ID != workspaceQueue.ID {
		t.Fatalf("workspace scope rows = %+v, want only queue %d", workspaceRows, workspaceQueue.ID)
	}

	collectionRows, err := repo.ListByScope(fixture.workspaceA, &fixture.collectionID)
	if err != nil {
		t.Fatalf("ListByScope collection: %v", err)
	}
	if len(collectionRows) != 1 || collectionRows[0].ID != collectionQueue.ID {
		t.Fatalf("collection scope rows = %+v, want only queue %d", collectionRows, collectionQueue.ID)
	}

	otherRows, err := repo.ListByScope(fixture.workspaceB, nil)
	if err != nil {
		t.Fatalf("ListByScope other workspace: %v", err)
	}
	if len(otherRows) != 1 || otherRows[0].ID != otherWorkspaceQueue.ID {
		t.Fatalf("other workspace rows = %+v, want only queue %d", otherRows, otherWorkspaceQueue.ID)
	}
}

func TestQueueRepositoryOrdersByPositionThenID(t *testing.T) {
	repo, fixture := setupQueueFixture(t)

	// Insert out of position order; equal positions must fall back to id.
	third, err := repo.Create(&models.Queue{WorkspaceID: fixture.workspaceA, Name: "third", QLQuery: "id > 0", Position: 5})
	if err != nil {
		t.Fatalf("create third: %v", err)
	}
	first, err := repo.Create(&models.Queue{WorkspaceID: fixture.workspaceA, Name: "first", QLQuery: "id > 0", Position: 1})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := repo.Create(&models.Queue{WorkspaceID: fixture.workspaceA, Name: "second", QLQuery: "id > 0", Position: 1})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	rows, err := repo.ListByScope(fixture.workspaceA, nil)
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	got := []int{rows[0].ID, rows[1].ID, rows[2].ID}
	want := []int{first.ID, second.ID, third.ID}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestQueueRepositoryUpdateAndDelete(t *testing.T) {
	repo, fixture := setupQueueFixture(t)

	queue, err := repo.Create(&models.Queue{WorkspaceID: fixture.workspaceA, Name: "before", QLQuery: "id > 0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	filterState := `{"priorities":[2]}`
	queue.Name = "after"
	queue.QLQuery = "statusCompleted = true"
	queue.FilterState = &filterState
	queue.Position = 7
	queue.IsHidden = true
	if err := repo.Update(queue); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByID(queue.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "after" || got.QLQuery != "statusCompleted = true" || got.Position != 7 || !got.IsHidden {
		t.Fatalf("updated queue = %+v", got)
	}
	if got.FilterState == nil || *got.FilterState != filterState {
		t.Fatalf("updated filter_state = %v", got.FilterState)
	}

	if err := repo.Delete(queue.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(queue.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
	}
}

func TestQueueRepositoryBuiltinDismissalsAreScopeUnique(t *testing.T) {
	repo, fixture := setupQueueFixture(t)
	builtinKey := "waiting"

	dismissal := &models.Queue{
		WorkspaceID: fixture.workspaceA,
		Name:        "Waiting",
		QLQuery:     "slaPaused = true AND statusCompleted = false",
		BuiltinKey:  &builtinKey,
		IsHidden:    true,
	}
	if _, err := repo.Create(dismissal); err != nil {
		t.Fatalf("create dismissal: %v", err)
	}
	if _, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceA, Name: "Waiting", QLQuery: "slaPaused = true",
		BuiltinKey: &builtinKey, IsHidden: true,
	}); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("duplicate dismissal error = %v, want ErrDuplicateEntry", err)
	}

	// The same preset may be dismissed independently in another scope.
	if _, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceA, CollectionID: &fixture.collectionID,
		Name: "Waiting", QLQuery: "slaPaused = true", BuiltinKey: &builtinKey, IsHidden: true,
	}); err != nil {
		t.Fatalf("dismiss same preset in collection scope: %v", err)
	}

	got, err := repo.GetByBuiltinKey(fixture.workspaceA, nil, builtinKey)
	if err != nil {
		t.Fatalf("GetByBuiltinKey: %v", err)
	}
	if got.BuiltinKey == nil || *got.BuiltinKey != builtinKey || !got.IsHidden {
		t.Fatalf("dismissal = %+v", got)
	}

	if err := repo.DeleteByBuiltinKey(fixture.workspaceA, nil, builtinKey); err != nil {
		t.Fatalf("DeleteByBuiltinKey: %v", err)
	}
	if _, err := repo.GetByBuiltinKey(fixture.workspaceA, nil, builtinKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByBuiltinKey after restore = %v, want ErrNotFound", err)
	}
	// The collection-scope dismissal is untouched.
	if _, err := repo.GetByBuiltinKey(fixture.workspaceA, &fixture.collectionID, builtinKey); err != nil {
		t.Fatalf("collection dismissal was removed: %v", err)
	}
}

func TestQueueRepositoryReorderIsScoped(t *testing.T) {
	repo, fixture := setupQueueFixture(t)

	a, err := repo.Create(&models.Queue{WorkspaceID: fixture.workspaceA, Name: "a", QLQuery: "id > 0", Position: 0})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := repo.Create(&models.Queue{WorkspaceID: fixture.workspaceA, Name: "b", QLQuery: "id > 0", Position: 1})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	outside, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceA, CollectionID: &fixture.collectionID,
		Name: "outside", QLQuery: "id > 0", Position: 0,
	})
	if err != nil {
		t.Fatalf("create outside: %v", err)
	}

	if err := repo.Reorder(fixture.workspaceA, nil, []int{b.ID, a.ID, outside.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	rows, err := repo.ListByScope(fixture.workspaceA, nil)
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != b.ID || rows[1].ID != a.ID {
		t.Fatalf("reordered rows = %+v, want [%d %d]", rows, b.ID, a.ID)
	}
	outsideRow, err := repo.GetByID(outside.ID)
	if err != nil {
		t.Fatalf("GetByID outside: %v", err)
	}
	if outsideRow.Position != 0 {
		t.Fatalf("out-of-scope queue position = %d, want 0", outsideRow.Position)
	}
}

func TestQueueRepositoryNextPosition(t *testing.T) {
	repo, fixture := setupQueueFixture(t)

	next, err := repo.NextPosition(fixture.workspaceA, nil)
	if err != nil {
		t.Fatalf("NextPosition empty: %v", err)
	}
	if next != 0 {
		t.Fatalf("empty NextPosition = %d, want 0", next)
	}
	if _, err := repo.Create(&models.Queue{WorkspaceID: fixture.workspaceA, Name: "a", QLQuery: "id > 0", Position: 4}); err != nil {
		t.Fatalf("create: %v", err)
	}
	next, err = repo.NextPosition(fixture.workspaceA, nil)
	if err != nil {
		t.Fatalf("NextPosition: %v", err)
	}
	if next != 5 {
		t.Fatalf("NextPosition = %d, want 5", next)
	}
	// A different scope is independent.
	next, err = repo.NextPosition(fixture.workspaceA, &fixture.collectionID)
	if err != nil {
		t.Fatalf("NextPosition collection: %v", err)
	}
	if next != 0 {
		t.Fatalf("collection NextPosition = %d, want 0", next)
	}
}

func TestQueueRepositoryCascadesOnScopeDeletion(t *testing.T) {
	repo, fixture := setupQueueFixture(t)

	if _, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceA, CollectionID: &fixture.collectionID,
		Name: "collection queue", QLQuery: "id > 0",
	}); err != nil {
		t.Fatalf("create collection queue: %v", err)
	}
	if _, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceA, Name: "workspace queue", QLQuery: "id > 0",
	}); err != nil {
		t.Fatalf("create workspace queue: %v", err)
	}

	// Deleting the collection removes its queues but keeps workspace-default ones.
	if _, err := repo.db.ExecWrite("DELETE FROM collections WHERE id = ?", fixture.collectionID); err != nil {
		t.Fatalf("delete collection: %v", err)
	}
	rows, err := repo.ListByScope(fixture.workspaceA, nil)
	if err != nil {
		t.Fatalf("ListByScope after collection delete: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "workspace queue" {
		t.Fatalf("workspace queues after collection delete = %+v", rows)
	}
	var collectionRows int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM queues WHERE collection_id = ?", fixture.collectionID).Scan(&collectionRows); err != nil {
		t.Fatalf("count collection queues: %v", err)
	}
	if collectionRows != 0 {
		t.Fatalf("collection queues = %d, want 0 after cascade", collectionRows)
	}
}

// TestQueueRepositoryGlobalCollectionScope covers a collection that has no
// single workspace: its saved views are scoped by collection id alone and must
// not leak into workspace-default or sibling-collection scopes.
func TestQueueRepositoryGlobalCollectionScope(t *testing.T) {
	repo, fixture := setupQueueFixture(t)
	globalCollectionID := testutils.InsertID(t, repo.db,
		`INSERT INTO collections (name, ql_query, workspace_id) VALUES ('Global queue collection', '', NULL)`)

	globalQueue, err := repo.Create(&models.Queue{
		CollectionID: &globalCollectionID,
		Name:         "Global view",
		QLQuery:      "id > 0",
	})
	if err != nil {
		t.Fatalf("Create global collection queue: %v", err)
	}
	if globalQueue.WorkspaceID != 0 {
		t.Fatalf("global queue workspace = %d, want 0", globalQueue.WorkspaceID)
	}
	got, err := repo.GetByID(globalQueue.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.WorkspaceID != 0 || got.CollectionID == nil || *got.CollectionID != globalCollectionID {
		t.Fatalf("global queue round-trip = %+v", got)
	}

	// A workspace-default queue and a workspace-collection queue must not leak
	// into the global collection scope.
	if _, err := repo.Create(&models.Queue{WorkspaceID: fixture.workspaceA, Name: "ws", QLQuery: "id > 0"}); err != nil {
		t.Fatalf("create workspace queue: %v", err)
	}
	if _, err := repo.Create(&models.Queue{
		WorkspaceID: fixture.workspaceA, CollectionID: &fixture.collectionID,
		Name: "ws collection", QLQuery: "id > 0",
	}); err != nil {
		t.Fatalf("create workspace collection queue: %v", err)
	}

	rows, err := repo.ListByScope(0, &globalCollectionID)
	if err != nil {
		t.Fatalf("ListByScope global: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != globalQueue.ID {
		t.Fatalf("global scope rows = %+v, want only queue %d", rows, globalQueue.ID)
	}

	next, err := repo.NextPosition(0, &globalCollectionID)
	if err != nil {
		t.Fatalf("NextPosition global: %v", err)
	}
	if next != 1 {
		t.Fatalf("global NextPosition = %d, want 1", next)
	}
}
