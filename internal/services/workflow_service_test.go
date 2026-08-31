package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"windshift/internal/constants"
	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// workflowTestEnv contains test data for workflow service tests
type workflowTestEnv struct {
	WorkspaceID int
	WorkflowID  int
	ConfigSetID int
	StatusID1   int
	StatusID2   int
	StatusID3   int
	CategoryID  int
}

// createWorkflowTestDB creates a test database for workflow service tests
func createWorkflowTestDB(t *testing.T) database.Database {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	return tdb.DB
}

// setupWorkflowTestEnv creates test data for workflow service tests
func setupWorkflowTestEnv(t *testing.T, db database.Database) workflowTestEnv {
	t.Helper()

	// Create workspace
	workspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES ('Workflow Test Workspace', 'WFL', 'Test workspace', TRUE, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	// Get existing status category
	var categoryID int
	err := db.QueryRow("SELECT id FROM status_categories LIMIT 1").Scan(&categoryID)
	if err != nil {
		t.Fatalf("Failed to get status category: %v", err)
	}

	// Create test statuses
	statusID1 := testutils.InsertID(t, db, `
		INSERT INTO statuses (name, description, category_id, is_default, created_at, updated_at)
		VALUES ('WF Open', 'Open status', ?, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, categoryID)

	statusID2 := testutils.InsertID(t, db, `
		INSERT INTO statuses (name, description, category_id, is_default, created_at, updated_at)
		VALUES ('WF In Progress', 'In progress status', ?, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, categoryID)

	statusID3 := testutils.InsertID(t, db, `
		INSERT INTO statuses (name, description, category_id, is_default, created_at, updated_at)
		VALUES ('WF Done', 'Done status', ?, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, categoryID)

	// Create test workflow
	workflowID := testutils.InsertID(t, db, `
		INSERT INTO workflows (name, description, is_default, created_at, updated_at)
		VALUES ('Test Workflow', 'A test workflow', FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	// Create workflow transitions
	// Initial transition (from NULL to Open)
	_, err = db.Exec(`
		INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order)
		VALUES (?, NULL, ?, 1)
	`, workflowID, statusID1)
	if err != nil {
		t.Fatalf("Failed to create initial transition: %v", err)
	}

	// Open -> In Progress
	_, err = db.Exec(`
		INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order)
		VALUES (?, ?, ?, 2)
	`, workflowID, statusID1, statusID2)
	if err != nil {
		t.Fatalf("Failed to create transition 1->2: %v", err)
	}

	// In Progress -> Done
	_, err = db.Exec(`
		INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order)
		VALUES (?, ?, ?, 3)
	`, workflowID, statusID2, statusID3)
	if err != nil {
		t.Fatalf("Failed to create transition 2->3: %v", err)
	}

	// Create a configuration set
	configSetID := testutils.InsertID(t, db, `
		INSERT INTO configuration_sets (name, workflow_id, created_at, updated_at)
		VALUES ('Test Config Set', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, workflowID)

	// Associate workspace with configuration set
	_, err = db.Exec(`
		INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id)
		VALUES (?, ?)
	`, workspaceID, configSetID)
	if err != nil {
		t.Fatalf("Failed to associate workspace with config set: %v", err)
	}

	return workflowTestEnv{
		WorkspaceID: workspaceID,
		WorkflowID:  workflowID,
		ConfigSetID: configSetID,
		StatusID1:   statusID1,
		StatusID2:   statusID2,
		StatusID3:   statusID3,
		CategoryID:  categoryID,
	}
}

func TestWorkflowService_GetWorkflowIDForItem(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	env := setupWorkflowTestEnv(t, db)

	t.Run("WithConfigSet", func(t *testing.T) {
		workflowID, err := service.GetWorkflowIDForItem(env.WorkspaceID, nil)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if workflowID == nil {
			t.Fatal("Expected non-nil workflow ID")
		}
		if *workflowID != env.WorkflowID {
			t.Errorf("Expected workflow ID %d, got %d", env.WorkflowID, *workflowID)
		}
	})

	t.Run("FallbackToDefaultWorkflow", func(t *testing.T) {
		// Create a workspace without a config set
		newWorkspaceID := testutils.InsertID(t, db, `
			INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
			VALUES ('No Config Workspace', 'NCW', 'No config set', TRUE, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`)

		// Mark workflow as default
		db.Exec("UPDATE workflows SET is_default = true WHERE id = ?", env.WorkflowID)

		workflowID, err := service.GetWorkflowIDForItem(newWorkspaceID, nil)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if workflowID == nil {
			t.Fatal("Expected non-nil workflow ID (should fallback to default)")
		}
	})
}

func TestWorkflowService_IsValidStatusTransition(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	env := setupWorkflowTestEnv(t, db)

	t.Run("SameStatusAlwaysValid", func(t *testing.T) {
		valid, err := service.IsValidStatusTransition(env.WorkspaceID, nil, int64(env.StatusID1), int64(env.StatusID1))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if !valid {
			t.Error("Expected same status transition to be valid")
		}
	})

	t.Run("ValidTransition", func(t *testing.T) {
		valid, err := service.IsValidStatusTransition(env.WorkspaceID, nil, int64(env.StatusID1), int64(env.StatusID2))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if !valid {
			t.Error("Expected Open -> In Progress transition to be valid")
		}
	})

	t.Run("InvalidTransition", func(t *testing.T) {
		valid, err := service.IsValidStatusTransition(env.WorkspaceID, nil, int64(env.StatusID1), int64(env.StatusID3))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if valid {
			t.Error("Expected Open -> Done transition to be invalid")
		}
	})
}

func TestWorkflowService_GetAvailableTransitions(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	env := setupWorkflowTestEnv(t, db)

	t.Run("FromOpenStatus", func(t *testing.T) {
		transitions, err := service.GetAvailableTransitions(env.WorkspaceID, nil, int64(env.StatusID1))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(transitions) != 1 {
			t.Errorf("Expected 1 transition from Open, got %d", len(transitions))
		}
	})

	t.Run("FromDoneStatus", func(t *testing.T) {
		transitions, err := service.GetAvailableTransitions(env.WorkspaceID, nil, int64(env.StatusID3))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(transitions) != 0 {
			t.Errorf("Expected 0 transitions from Done, got %d", len(transitions))
		}
	})
}

func TestWorkflowService_GetInitialStatusID(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	env := setupWorkflowTestEnv(t, db)

	t.Run("ReturnsInitialStatus", func(t *testing.T) {
		statusID, err := service.GetInitialStatusID(env.WorkflowID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if statusID == nil {
			t.Fatal("Expected non-nil initial status ID")
		}
		if *statusID != env.StatusID1 {
			t.Errorf("Expected initial status ID %d, got %d", env.StatusID1, *statusID)
		}
	})

	t.Run("WorkflowWithoutInitialStatus", func(t *testing.T) {
		newWorkflowID := testutils.InsertID(t, db, `
			INSERT INTO workflows (name, description, is_default, created_at, updated_at)
			VALUES ('No Initial Workflow', 'No initial', FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`)

		statusID, err := service.GetInitialStatusID(newWorkflowID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if statusID != nil {
			t.Errorf("Expected nil initial status ID, got %d", *statusID)
		}
	})
}

func TestWorkflowService_List(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	_ = setupWorkflowTestEnv(t, db)

	workflows, err := service.List()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(workflows) == 0 {
		t.Error("Expected at least one workflow")
	}
}

func TestWorkflowService_GetByID(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	env := setupWorkflowTestEnv(t, db)

	t.Run("Success", func(t *testing.T) {
		workflow, err := service.GetByID(env.WorkflowID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if workflow.ID != env.WorkflowID {
			t.Errorf("Expected workflow ID %d, got %d", env.WorkflowID, workflow.ID)
		}
		if workflow.Name != "Test Workflow" {
			t.Errorf("Expected workflow name 'Test Workflow', got '%s'", workflow.Name)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := service.GetByID(99999)
		if err == nil {
			t.Error("Expected error for non-existent workflow")
		}
	})
}

func TestWorkflowService_Exists(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	env := setupWorkflowTestEnv(t, db)

	t.Run("ExistingWorkflow", func(t *testing.T) {
		exists, err := service.Exists(env.WorkflowID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if !exists {
			t.Error("Expected workflow to exist")
		}
	})

	t.Run("NonExistentWorkflow", func(t *testing.T) {
		exists, err := service.Exists(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if exists {
			t.Error("Expected workflow to not exist")
		}
	})
}

func TestWorkflowService_GetTransitions(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	env := setupWorkflowTestEnv(t, db)

	t.Run("ReturnsAllTransitions", func(t *testing.T) {
		transitions, err := service.GetTransitions(env.WorkflowID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// We created 3 transitions: initial, Open->InProgress, InProgress->Done
		if len(transitions) != 3 {
			t.Errorf("Expected 3 transitions, got %d", len(transitions))
		}
	})

	t.Run("EmptyForNoTransitions", func(t *testing.T) {
		newWorkflowID := testutils.InsertID(t, db, `
			INSERT INTO workflows (name, description, is_default, created_at, updated_at)
			VALUES ('Empty Transitions Workflow', 'No transitions', FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`)

		transitions, err := service.GetTransitions(newWorkflowID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(transitions) != 0 {
			t.Errorf("Expected 0 transitions, got %d", len(transitions))
		}
	})
}

func TestWorkflowService_GetTransitionsFromStatus(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)
	env := setupWorkflowTestEnv(t, db)

	t.Run("ReturnsTransitionsFromOpenStatus", func(t *testing.T) {
		transitions, err := service.GetTransitionsFromStatus(env.StatusID1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(transitions) != 1 {
			t.Fatalf("Expected exactly the directed transition, got %d", len(transitions))
		}
		if transitions[0].FromStatusID == nil || *transitions[0].FromStatusID != env.StatusID1 {
			t.Fatalf("Expected transition from status %d, got %#v", env.StatusID1, transitions[0])
		}
	})

	t.Run("ReturnsEmptyForFinalStatus", func(t *testing.T) {
		transitions, err := service.GetTransitionsFromStatus(env.StatusID3)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(transitions) != 0 {
			t.Fatalf("Expected no transitions from Done, got %#v", transitions)
		}
	})

	t.Run("ExcludesCreationOnlyInitialTransitions", func(t *testing.T) {
		transitions, err := service.GetTransitionsFromStatus(env.StatusID1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		for _, transition := range transitions {
			if transition.FromStatusID == nil {
				t.Fatal("Initial transition leaked into moves for an existing item")
			}
		}
	})

	t.Run("NonExistentStatus", func(t *testing.T) {
		transitions, err := service.GetTransitionsFromStatus(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if transitions == nil || len(transitions) != 0 {
			t.Fatalf("Expected a non-nil empty result, got %#v", transitions)
		}
	})
}

func TestWorkflowService_GetWorkflowIDForItem_NoWorkspace(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)

	// Try to get workflow for non-existent workspace
	workflowID, err := service.GetWorkflowIDForItem(99999, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should return nil or fall back to default workflow
	// (depending on whether default workflow exists)
	_ = workflowID // Result depends on whether default workflow exists
}

func TestWorkflowService_IsValidStatusTransition_NoWorkflow(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)

	// Create workspace without config set
	workspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES ('No Workflow Workspace', 'NWW', 'No workflow', TRUE, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	// Remove default workflow
	db.Exec("UPDATE workflows SET is_default = false")

	// Without any workflow, any transition should be allowed
	valid, err := service.IsValidStatusTransition(workspaceID, nil, 1, 2)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !valid {
		t.Error("Expected transition to be valid when no workflow is configured")
	}
}

func TestWorkflowService_PersonalTaskWithMissingStatusCanTransition(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	data := tdb.SeedTestData(t)
	db := tdb.DB

	personalWorkspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Legacy personal workspace', 'LEGACY-PERSONAL', TRUE, TRUE, ?)
	`, data.UserID)
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: personalWorkspaceID,
		CreatorID:   &data.UserID,
		Title:       "Legacy personal task",
	})
	if err != nil {
		t.Fatalf("create personal task: %v", err)
	}
	itemID := int(itemID64)

	// Simulate a legacy personal task created before personal items received an
	// implicit Open status.
	if _, err := db.Exec(`UPDATE items SET status_id = NULL WHERE id = ?`, itemID); err != nil {
		t.Fatalf("clear legacy task status: %v", err)
	}

	result, err := NewWorkflowService(db).PerformTransition(
		context.Background(),
		PerformTransitionRequest{
			ItemID:      itemID,
			ToStatusID:  constants.StatusIDDone,
			ActorUserID: data.UserID,
		},
		repository.NewItemRepository(db),
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("transition legacy personal task: %v", err)
	}
	if result.OldStatusID == nil || *result.OldStatusID != constants.StatusIDOpen {
		t.Fatalf("old status = %v, want implicit Open (%d)", result.OldStatusID, constants.StatusIDOpen)
	}
	if result.NewStatusID == nil || *result.NewStatusID != constants.StatusIDDone {
		t.Fatalf("new status = %v, want Done (%d)", result.NewStatusID, constants.StatusIDDone)
	}
	if result.Item == nil || result.Item.StatusID == nil || *result.Item.StatusID != constants.StatusIDDone {
		t.Fatalf("result item status = %v, want Done (%d)", result.Item, constants.StatusIDDone)
	}

	var storedStatusID int
	if err := db.QueryRow(`SELECT status_id FROM items WHERE id = ?`, itemID).Scan(&storedStatusID); err != nil {
		t.Fatalf("load stored status: %v", err)
	}
	if storedStatusID != constants.StatusIDDone {
		t.Fatalf("stored status = %d, want Done (%d)", storedStatusID, constants.StatusIDDone)
	}

	var oldValue, newValue string
	if err := db.QueryRow(`
		SELECT old_value, new_value FROM item_history
		WHERE item_id = ? AND field_name = 'status_id'
		ORDER BY id DESC LIMIT 1
	`, itemID).Scan(&oldValue, &newValue); err != nil {
		t.Fatalf("load transition history: %v", err)
	}
	if oldValue != "1" || newValue != "3" {
		t.Fatalf("transition history = %q -> %q, want %d -> %d", oldValue, newValue, constants.StatusIDOpen, constants.StatusIDDone)
	}
	var eventType, payloadJSON string
	if err := db.QueryRow(`
		SELECT event_type, payload FROM domain_events
		WHERE aggregate_type = 'item' AND aggregate_id = ?
		ORDER BY aggregate_sequence DESC LIMIT 1
	`, itemID).Scan(&eventType, &payloadJSON); err != nil {
		t.Fatalf("load status domain event: %v", err)
	}
	var payload itemevents.StatusChangedV1
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode status domain event: %v", err)
	}
	if eventType != itemevents.StatusChanged || payload.OldStatusID == nil || *payload.OldStatusID != constants.StatusIDOpen || payload.NewStatusID == nil || *payload.NewStatusID != constants.StatusIDDone || len(payload.Changes) != 1 || payload.Changes[0].Field != "status_id" {
		t.Fatalf("status domain event = %s %+v", eventType, payload)
	}
}

func TestWorkflowService_GetAvailableTransitions_NoWorkflow(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)

	// Create workspace without config set
	workspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES ('No Workflow Workspace 2', 'NW2', 'No workflow', TRUE, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	// Remove default workflow
	db.Exec("UPDATE workflows SET is_default = false")

	transitions, err := service.GetAvailableTransitions(workspaceID, nil, 1)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should return empty slice when no workflow configured
	if transitions == nil {
		t.Error("Expected empty slice, not nil")
	}
	if len(transitions) != 0 {
		t.Errorf("Expected 0 transitions, got %d", len(transitions))
	}
}

func TestWorkflowService_GetInitialStatusID_NonExistentWorkflow(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)

	statusID, err := service.GetInitialStatusID(99999)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if statusID != nil {
		t.Errorf("Expected nil initial status for non-existent workflow, got %d", *statusID)
	}
}

func TestWorkflowService_GetByID_ZeroID(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)

	_, err := service.GetByID(0)
	if err == nil {
		t.Error("Expected error for zero ID")
	}
}

func TestWorkflowService_GetByID_NegativeID(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)

	_, err := service.GetByID(-1)
	if err == nil {
		t.Error("Expected error for negative ID")
	}
}

func TestWorkflowService_Exists_ZeroID(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)

	exists, err := service.Exists(0)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if exists {
		t.Error("Expected false for zero ID")
	}
}

func TestWorkflowService_GetTransitions_ReturnsEmptySliceNotNil(t *testing.T) {
	db := createWorkflowTestDB(t)

	service := NewWorkflowService(db)

	// Create workflow without transitions
	workflowID := testutils.InsertID(t, db, `
		INSERT INTO workflows (name, description, is_default, created_at, updated_at)
		VALUES ('Empty Workflow', 'No transitions', FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	transitions, err := service.GetTransitions(workflowID)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if transitions == nil {
		t.Error("Expected empty slice, got nil")
	}
}

func TestWorkflowService_List_ReturnsEmptySliceNotNil(t *testing.T) {
	db := createWorkflowTestDB(t)

	// Delete all workflows
	db.Exec("DELETE FROM workflow_transitions")
	db.Exec("DELETE FROM configuration_sets")
	db.Exec("DELETE FROM workflows")

	service := NewWorkflowService(db)

	workflows, err := service.List()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if workflows == nil {
		t.Error("Expected empty slice, got nil")
	}
}

func TestWorkflowService_GetTransitionsFromStatus_ReturnsEmptySliceNotNil(t *testing.T) {
	db := createWorkflowTestDB(t)

	// Delete all transitions
	db.Exec("DELETE FROM workflow_transitions")

	service := NewWorkflowService(db)

	transitions, err := service.GetTransitionsFromStatus(1)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if transitions == nil {
		t.Error("Expected empty slice, got nil")
	}
}

func cacheLen(s *WorkflowService) int {
	n := 0
	s.initialStatusCache.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

func TestMaybeSweepInitialStatusCacheDropsExpired(t *testing.T) {
	s := &WorkflowService{}
	now := time.Now()
	for i := 0; i < 100; i++ {
		key := initialStatusCacheKey(i, nil)
		s.initialStatusCache.Store(key, &initialStatusCacheEntry{
			expiresAt: now.Add(-time.Second),
		})
	}
	for i := 100; i < 110; i++ {
		key := initialStatusCacheKey(i, nil)
		s.initialStatusCache.Store(key, &initialStatusCacheEntry{
			expiresAt: now.Add(time.Hour),
		})
	}

	s.maybeSweepInitialStatusCache(now)

	if got := cacheLen(s); got != 10 {
		t.Fatalf("after sweep, want 10 live entries, got %d", got)
	}
}

func TestMaybeSweepInitialStatusCacheThrottles(t *testing.T) {
	s := &WorkflowService{}
	now := time.Now()
	for i := 0; i < 5; i++ {
		key := initialStatusCacheKey(i, nil)
		s.initialStatusCache.Store(key, &initialStatusCacheEntry{
			expiresAt: now.Add(-time.Second),
		})
	}

	s.maybeSweepInitialStatusCache(now)
	if got := cacheLen(s); got != 0 {
		t.Fatalf("first sweep should evict, got %d", got)
	}

	for i := 5; i < 10; i++ {
		key := initialStatusCacheKey(i, nil)
		s.initialStatusCache.Store(key, &initialStatusCacheEntry{
			expiresAt: now.Add(-time.Second),
		})
	}

	s.maybeSweepInitialStatusCache(now.Add(time.Second))
	if got := cacheLen(s); got != 5 {
		t.Fatalf("second sweep within throttle window should be a no-op, got %d", got)
	}

	s.maybeSweepInitialStatusCache(now.Add(initialStatusSweepInterval + time.Second))
	if got := cacheLen(s); got != 0 {
		t.Fatalf("third sweep past throttle window should evict, got %d", got)
	}
}

func TestSlugifyStatusName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"In Review", "in-review"},
		{"IN REVIEW", "in-review"},
		{"in-review", "in-review"},
		{"in--review", "in-review"},
		{"  in review  ", "in-review"},
		{"start review / ready", "start-review-ready"},
		{"#close", "close"},
		{"", ""},
		{"   ", ""},
		{"Done!", "done"},
		{"v1.0", "v1-0"},
	}
	for _, c := range cases {
		if got := slugifyStatusName(c.in); got != c.want {
			t.Errorf("slugifyStatusName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
