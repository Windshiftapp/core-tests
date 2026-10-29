//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

type bulkTransitionDeleteEnv struct {
	app        *ItemApplicationService
	db         database.Database
	workspace  int
	adminID    int
	deniedID   int
	itemTypeID int
	openStatus int
	doneStatus int
}

// newBulkTransitionDeleteEnv wires a workspace with one workflow transition
// (open -> done) so the application service can exercise the production
// workflow validation path.
func newBulkTransitionDeleteEnv(t *testing.T) bulkTransitionDeleteEnv {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	insertID := func(query string, args ...any) int {
		t.Helper()
		return testutils.InsertID(t, tdb.GetDatabase(), query, args...)
	}

	var categoryID int
	if err := tdb.QueryRow(`SELECT id FROM status_categories LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("load status category: %v", err)
	}
	doneStatus := insertID(`INSERT INTO statuses (name, category_id) VALUES ('Bulk done', ?)`, categoryID)
	workflowID := insertID(`INSERT INTO workflows (name, description, is_default) VALUES ('bulk-transition', '', false)`)
	insertID(`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, workflowID, data.StatusID, doneStatus)
	configurationSetID := insertID(`INSERT INTO configuration_sets (name, workflow_id) VALUES ('bulk-transition', ?)`, workflowID)
	insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, data.WorkspaceID, configurationSetID)
	var defaultItemTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE is_default = true LIMIT 1`).Scan(&defaultItemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	insertID(`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id) VALUES (?, ?)`, configurationSetID, defaultItemTypeID)

	deniedID := insertID(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('bulk-denied@example.test', 'bulk-denied', 'Bulk', 'Denied', true)
	`)
	// An explicit viewer assignment locks the workspace down, so this user has
	// no item.edit/item.delete even though the workspace is otherwise open.
	var viewerRoleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE builtin_key = 'viewer' LIMIT 1`).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load viewer role: %v", err)
	}
	insertID(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, deniedID, data.WorkspaceID, viewerRoleID)

	permissions, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	creation := NewItemCreationService(tdb.GetDatabase(), permissions)
	update := NewItemUpdateApplicationService(tdb.GetDatabase(), permissions)
	deletion := NewItemDeletionApplicationService(tdb.GetDatabase(), permissions)
	app := NewItemApplicationService(tdb.GetDatabase(), permissions, creation, update, deletion)

	return bulkTransitionDeleteEnv{
		app:        app,
		db:         tdb.GetDatabase(),
		workspace:  data.WorkspaceID,
		adminID:    data.UserID,
		deniedID:   deniedID,
		itemTypeID: defaultItemTypeID,
		openStatus: data.StatusID,
		doneStatus: doneStatus,
	}
}

func (env bulkTransitionDeleteEnv) createItem(t *testing.T, title string, statusID int) int {
	t.Helper()
	itemTypeID := env.itemTypeID
	itemID, err := CreateItem(env.db, ItemCreationParams{
		WorkspaceID: env.workspace,
		ItemTypeID:  &itemTypeID,
		Title:       title,
		StatusID:    &statusID,
	})
	if err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return int(itemID)
}

func TestBulkTransitionMovesValidItemsAndReportsRejections(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	actor := AuditActor{UserID: env.adminID, Username: "bulk-admin"}
	first := env.createItem(t, "Move me one", env.openStatus)
	second := env.createItem(t, "Move me two", env.openStatus)
	// The workflow only defines open -> done, so an item sitting on a status
	// with no outgoing edge is a per-item failure, not a batch-wide error.
	var categoryID int
	if err := env.db.QueryRow(`SELECT id FROM status_categories LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("load status category: %v", err)
	}
	orphan := env.createItem(t, "No outgoing edge", testutils.InsertID(t, env.db,
		`INSERT INTO statuses (name, category_id) VALUES ('Bulk orphan', ?)`, categoryID))

	result, err := env.app.BulkTransition(context.Background(), actor, []int{first, second, orphan}, env.doneStatus)
	if err != nil {
		t.Fatalf("BulkTransition: %v", err)
	}
	if result.RequestedCount != 3 || result.TransitionedCount != 2 || result.UnchangedCount != 0 || result.FailedCount != 1 {
		t.Fatalf("result = requested:%d transitioned:%d unchanged:%d failed:%d, want 3/2/0/1",
			result.RequestedCount, result.TransitionedCount, result.UnchangedCount, result.FailedCount)
	}
	if len(result.Failures) != 1 || result.Failures[0].ItemID != orphan || result.Failures[0].Code != "workflow_invalid" {
		t.Fatalf("failures = %#v, want workflow_invalid for item %d", result.Failures, orphan)
	}
	for _, id := range []int{first, second} {
		var statusID int
		if err := env.db.QueryRow(`SELECT status_id FROM items WHERE id = ?`, id).Scan(&statusID); err != nil {
			t.Fatalf("load item %d: %v", id, err)
		}
		if statusID != env.doneStatus {
			t.Fatalf("item %d status = %d, want %d", id, statusID, env.doneStatus)
		}
	}
}

func TestBulkTransitionRepeatsAreNoOps(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	actor := AuditActor{UserID: env.adminID, Username: "bulk-admin"}
	itemID := env.createItem(t, "Transition once", env.openStatus)

	if _, err := env.app.BulkTransition(context.Background(), actor, []int{itemID}, env.doneStatus); err != nil {
		t.Fatalf("first BulkTransition: %v", err)
	}
	retry, err := env.app.BulkTransition(context.Background(), actor, []int{itemID}, env.doneStatus)
	if err != nil {
		t.Fatalf("retry BulkTransition: %v", err)
	}
	if retry.TransitionedCount != 0 || retry.UnchangedCount != 1 || retry.FailedCount != 0 {
		t.Fatalf("retry = transitioned:%d unchanged:%d failed:%d, want 0/1/0",
			retry.TransitionedCount, retry.UnchangedCount, retry.FailedCount)
	}
}

func TestBulkTransitionReportsPermissionDenialPerItem(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	admin := AuditActor{UserID: env.adminID, Username: "bulk-admin"}
	itemID := env.createItem(t, "Protected", env.openStatus)

	denied := AuditActor{UserID: env.deniedID, Username: "bulk-denied"}
	result, err := env.app.BulkTransition(context.Background(), denied, []int{itemID}, env.doneStatus)
	if err != nil {
		t.Fatalf("BulkTransition: %v", err)
	}
	if result.TransitionedCount != 0 || result.FailedCount != 1 || result.Failures[0].Code != "forbidden" {
		t.Fatalf("denied result = %#v, want one forbidden failure", result)
	}
	var statusID int
	if err := env.db.QueryRow(`SELECT status_id FROM items WHERE id = ?`, itemID).Scan(&statusID); err != nil {
		t.Fatalf("load item: %v", err)
	}
	if statusID != env.openStatus {
		t.Fatalf("denied transition changed status to %d", statusID)
	}

	if _, err := env.app.BulkTransition(context.Background(), admin, []int{itemID}, env.doneStatus); err != nil {
		t.Fatalf("admin BulkTransition: %v", err)
	}
}

func TestBulkTransitionRejectsEmptyAndOversizedBatches(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	actor := AuditActor{UserID: env.adminID, Username: "bulk-admin"}
	if _, err := env.app.BulkTransition(context.Background(), actor, nil, env.doneStatus); !errors.Is(err, ErrBulkItemNotFound) {
		t.Fatalf("empty batch error = %v, want ErrBulkItemNotFound", err)
	}
	oversized := make([]int, MaxBulkItemUpdates+1)
	for i := range oversized {
		oversized[i] = i + 1
	}
	if _, err := env.app.BulkTransition(context.Background(), actor, oversized, env.doneStatus); !errors.Is(err, ErrBulkItemLimit) {
		t.Fatalf("oversized batch error = %v, want ErrBulkItemLimit", err)
	}
	itemID := env.createItem(t, "Needs a target status", env.openStatus)
	var validationErr *validation.ValidationError
	if _, err := env.app.BulkTransition(context.Background(), actor, []int{itemID}, 0); !errors.As(err, &validationErr) || validationErr.Field != "to_status_id" {
		t.Fatalf("missing target error = %v, want to_status_id validation error", err)
	}
}

func TestBulkDeleteRemovesEveryPermittedItem(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	actor := AuditActor{UserID: env.adminID, Username: "bulk-admin"}
	first := env.createItem(t, "Delete me one", env.openStatus)
	second := env.createItem(t, "Delete me two", env.openStatus)

	result, err := env.app.BulkDelete(actor, []int{first, second})
	if err != nil {
		t.Fatalf("BulkDelete: %v", err)
	}
	if result.RequestedCount != 2 || result.DeletedCount != 2 || result.FailedCount != 0 {
		t.Fatalf("result = requested:%d deleted:%d failed:%d, want 2/2/0",
			result.RequestedCount, result.DeletedCount, result.FailedCount)
	}
	var remaining int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM items WHERE id IN (?, ?)`, first, second).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining items = %d, want 0", remaining)
	}
}

func TestBulkDeleteReportsPermissionAndMissingItems(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	itemID := env.createItem(t, "Keep me", env.openStatus)

	denied := AuditActor{UserID: env.deniedID, Username: "bulk-denied"}
	result, err := env.app.BulkDelete(denied, []int{itemID})
	if err != nil {
		t.Fatalf("denied BulkDelete: %v", err)
	}
	if result.DeletedCount != 0 || result.FailedCount != 1 || result.Failures[0].Code != "forbidden" {
		t.Fatalf("denied result = %#v, want one forbidden failure", result)
	}

	admin := AuditActor{UserID: env.adminID, Username: "bulk-admin"}
	missing, err := env.app.BulkDelete(admin, []int{itemID, 999999})
	if err != nil {
		t.Fatalf("BulkDelete with missing item: %v", err)
	}
	if missing.DeletedCount != 1 || missing.FailedCount != 1 || missing.Failures[0].Code != "not_found" {
		t.Fatalf("missing result = %#v, want one deleted and one not_found", missing)
	}
}

func TestBulkDeleteRejectsEmptyAndOversizedBatches(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	actor := AuditActor{UserID: env.adminID, Username: "bulk-admin"}
	if _, err := env.app.BulkDelete(actor, nil); !errors.Is(err, ErrBulkItemNotFound) {
		t.Fatalf("empty batch error = %v, want ErrBulkItemNotFound", err)
	}
	oversized := make([]int, MaxBulkItemUpdates+1)
	for i := range oversized {
		oversized[i] = i + 1
	}
	if _, err := env.app.BulkDelete(actor, oversized); !errors.Is(err, ErrBulkItemLimit) {
		t.Fatalf("oversized batch error = %v, want ErrBulkItemLimit", err)
	}
}

// Guard against the env helper silently dropping the workflow wiring: a
// transition from a status with no configured edge must be rejected.
func TestBulkTransitionWithoutWorkflowEdgeFailsEveryItem(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	actor := AuditActor{UserID: env.adminID, Username: "bulk-admin"}
	var categoryID int
	if err := env.db.QueryRow(`SELECT id FROM status_categories LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("load category: %v", err)
	}
	isolated := env.createItem(t, "No edge", env.openStatus)
	// A second item at the target status, then transition it to a third status
	// that has no outgoing edge.
	orphanStatusID := testutils.InsertID(t, env.db,
		`INSERT INTO statuses (name, category_id) VALUES ('Orphan', ?)`, categoryID)
	result, err := env.app.BulkTransition(context.Background(), actor, []int{isolated}, orphanStatusID)
	if err != nil {
		t.Fatalf("BulkTransition: %v", err)
	}
	if result.FailedCount != 1 || result.Failures[0].Code != "workflow_invalid" {
		t.Fatalf("result = %#v, want workflow_invalid failure", result)
	}
	if result.Failures[0].Message == "" {
		t.Fatalf("failure message should explain the rejection: %#v", result.Failures[0])
	}
}
