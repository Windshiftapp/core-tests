package services

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

func newPlanningScopeTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "planning-scope.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return db
}

func planningScopeInsertID(t *testing.T, db database.Database, query string, args ...interface{}) int {
	t.Helper()
	return testutils.InsertID(t, db, query, args...)
}

// seedPlanningScopeItem creates an item through the production CreateItem
// path with a fixed creation timestamp, preserving the reported history shape.
func seedPlanningScopeItem(t *testing.T, db database.Database, workspaceID int, title string, statusID, iterationID int) int {
	t.Helper()
	created, err := time.Parse("2006-01-02 15:04:05", "2026-07-01 12:00:00")
	if err != nil {
		t.Fatalf("parse fixture timestamp: %v", err)
	}
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		Title:       title,
		StatusID:    &statusID,
		IterationID: &iterationID,
		CreatedAt:   &created,
		UpdatedAt:   &created,
	})
	if err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return int(itemID)
}

type planningScopeFixture struct {
	db          database.Database
	workspaceA  int
	workspaceB  int
	milestoneID int
	iterationID int
	itemA       int
}

func seedPlanningScopeFixture(t *testing.T) planningScopeFixture {
	t.Helper()
	db := newPlanningScopeTestDB(t)
	workspaceA := planningScopeInsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Planning A', 'PLA', '', true, false)
	`)
	workspaceB := planningScopeInsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Planning B', 'PLB', '', true, false)
	`)
	categoryID := planningScopeInsertID(t, db, `
		INSERT INTO status_categories (name, color, description, is_completed)
		VALUES ('Planning open', '#123456', '', false)
	`)
	statusID := planningScopeInsertID(t, db, `
		INSERT INTO statuses (name, description, category_id)
		VALUES ('Planning Open', '', ?)
	`, categoryID)
	milestoneID := planningScopeInsertID(t, db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Shared milestone', '', 'planning', true, NULL)
	`)
	iterationID := planningScopeInsertID(t, db, `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Shared iteration', '', '2026-07-01', '2026-07-31', 'active', true, NULL)
	`)
	// Items are created through the production path so they carry canonical
	// ranks; the fixture keeps identical titles and the July 1 creation time.
	itemA := seedPlanningScopeItem(t, db, workspaceA, "Visible item", statusID, iterationID)
	itemB := seedPlanningScopeItem(t, db, workspaceB, "Hidden item", statusID, iterationID)
	if _, err := db.ExecWrite(`INSERT INTO item_milestones (item_id, milestone_id) VALUES (?, ?), (?, ?)`, itemA, milestoneID, itemB, milestoneID); err != nil {
		t.Fatalf("attach milestone items: %v", err)
	}
	planningScopeInsertID(t, db, `
		INSERT INTO test_sets (workspace_id, name, description, milestone_id)
		VALUES (?, 'Visible tests', '', ?)
	`, workspaceA, milestoneID)
	planningScopeInsertID(t, db, `
		INSERT INTO test_sets (workspace_id, name, description, milestone_id)
		VALUES (?, 'Hidden tests', '', ?)
	`, workspaceB, milestoneID)
	return planningScopeFixture{db: db, workspaceA: workspaceA, workspaceB: workspaceB, milestoneID: milestoneID, iterationID: iterationID, itemA: itemA}
}

func TestPlanningReportsRespectVisibleWorkspaces(t *testing.T) {
	fixture := seedPlanningScopeFixture(t)
	service := NewPlanningService(fixture.db)

	milestone, err := service.GetMilestoneProgress(fixture.milestoneID, []int{fixture.workspaceA})
	if err != nil {
		t.Fatalf("GetMilestoneProgress: %v", err)
	}
	if milestone.TotalItems != 1 {
		t.Fatalf("milestone total = %d, want 1", milestone.TotalItems)
	}
	for _, items := range milestone.ItemsByCategory {
		for _, item := range items {
			if item.WorkspaceID != fixture.workspaceA || item.Title == "Hidden item" {
				t.Fatalf("milestone leaked hidden item: %+v", item)
			}
		}
	}

	iteration, err := service.GetIterationProgress(fixture.iterationID, []int{fixture.workspaceA})
	if err != nil {
		t.Fatalf("GetIterationProgress: %v", err)
	}
	if iteration.TotalItems != 1 {
		t.Fatalf("iteration total = %d, want 1", iteration.TotalItems)
	}

	burndown, err := service.GetIterationBurndown(fixture.iterationID, []int{fixture.workspaceA})
	if err != nil {
		t.Fatalf("GetIterationBurndown: %v", err)
	}
	if burndown.TotalItems != 1 {
		t.Fatalf("burndown total = %d, want 1", burndown.TotalItems)
	}

	stats, err := service.GetMilestoneTestStatistics(fixture.milestoneID, []int{fixture.workspaceA})
	if err != nil {
		t.Fatalf("GetMilestoneTestStatistics: %v", err)
	}
	if stats.TotalTestPlans != 1 {
		t.Fatalf("test plan total = %d, want 1", stats.TotalTestPlans)
	}

	empty, err := service.GetMilestoneProgress(fixture.milestoneID, nil)
	if err != nil {
		t.Fatalf("GetMilestoneProgress empty scope: %v", err)
	}
	if empty.TotalItems != 0 || len(empty.ItemsByCategory) != 0 {
		t.Fatalf("empty workspace scope returned items: %+v", empty)
	}
}

func TestPlanningListsRespectVisibleWorkspaces(t *testing.T) {
	fixture := seedPlanningScopeFixture(t)
	service := NewPlanningService(fixture.db)
	localMilestoneA := planningScopeInsertID(t, fixture.db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Local milestone A', '', 'planning', false, ?)
	`, fixture.workspaceA)
	planningScopeInsertID(t, fixture.db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Local milestone B', '', 'planning', false, ?)
	`, fixture.workspaceB)
	localIterationA := planningScopeInsertID(t, fixture.db, `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Local iteration A', '', '2026-07-01', '2026-07-31', 'active', false, ?)
	`, fixture.workspaceA)
	planningScopeInsertID(t, fixture.db, `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Local iteration B', '', '2026-07-01', '2026-07-31', 'active', false, ?)
	`, fixture.workspaceB)

	milestones, total, err := service.ListMilestones(MilestoneListParams{
		Limit: 100, WorkspaceIDs: []int{fixture.workspaceA}, IncludeGlobal: true,
	})
	if err != nil {
		t.Fatalf("ListMilestones: %v", err)
	}
	if total != 2 || len(milestones) != 2 {
		t.Fatalf("milestone list len/total = %d/%d, want 2/2", len(milestones), total)
	}
	seenLocalMilestone := false
	for _, milestone := range milestones {
		if milestone.WorkspaceID != nil && *milestone.WorkspaceID != fixture.workspaceA {
			t.Fatalf("milestone list leaked workspace %d", *milestone.WorkspaceID)
		}
		seenLocalMilestone = seenLocalMilestone || milestone.ID == localMilestoneA
	}
	if !seenLocalMilestone {
		t.Fatal("visible local milestone missing")
	}

	iterations, total, err := service.ListIterations(IterationListParams{
		Limit: 100, WorkspaceIDs: []int{fixture.workspaceA}, IncludeGlobal: true,
	})
	if err != nil {
		t.Fatalf("ListIterations: %v", err)
	}
	if total != 2 || len(iterations) != 2 {
		t.Fatalf("iteration list len/total = %d/%d, want 2/2", len(iterations), total)
	}
	seenLocalIteration := false
	for _, iteration := range iterations {
		if iteration.WorkspaceID != nil && *iteration.WorkspaceID != fixture.workspaceA {
			t.Fatalf("iteration list leaked workspace %d", *iteration.WorkspaceID)
		}
		seenLocalIteration = seenLocalIteration || iteration.ID == localIterationA
	}
	if !seenLocalIteration {
		t.Fatal("visible local iteration missing")
	}

	_, emptyTotal, err := service.ListMilestones(MilestoneListParams{Limit: 100})
	if err != nil {
		t.Fatalf("ListMilestones empty scope: %v", err)
	}
	if emptyTotal != 0 {
		t.Fatalf("unscoped local milestone list total = %d, want 0", emptyTotal)
	}
}

func TestPlanningCreateRejectsInvalidScope(t *testing.T) {
	db := newPlanningScopeTestDB(t)
	service := NewPlanningService(db)
	workspaceID := planningScopeInsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Scope workspace', 'SCP', '', true, false)
	`)

	if _, err := service.CreateMilestone(CreateMilestoneParams{Name: "Orphan local"}); !errors.Is(err, ErrInvalidPlanningScope) {
		t.Fatalf("CreateMilestone orphan error = %v, want ErrInvalidPlanningScope", err)
	}
	if _, err := service.CreateMilestone(CreateMilestoneParams{Name: "Global with workspace", IsGlobal: true, WorkspaceID: &workspaceID}); !errors.Is(err, ErrInvalidPlanningScope) {
		t.Fatalf("CreateMilestone mixed scope error = %v, want ErrInvalidPlanningScope", err)
	}
	if _, err := service.CreateIteration(CreateIterationParams{Name: "Orphan local", StartDate: "2026-07-01", EndDate: "2026-07-31"}); !errors.Is(err, ErrInvalidPlanningScope) {
		t.Fatalf("CreateIteration orphan error = %v, want ErrInvalidPlanningScope", err)
	}
	if _, err := service.CreateIteration(CreateIterationParams{Name: "Global with workspace", StartDate: "2026-07-01", EndDate: "2026-07-31", IsGlobal: true, WorkspaceID: &workspaceID}); !errors.Is(err, ErrInvalidPlanningScope) {
		t.Fatalf("CreateIteration mixed scope error = %v, want ErrInvalidPlanningScope", err)
	}

	if _, err := db.ExecWrite(`
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Direct orphan', '', 'planning', false, NULL)
	`); err == nil {
		t.Fatal("database accepted an invalid milestone scope")
	}
}

func TestItemPlanningAssignmentsRejectCrossWorkspaceObjects(t *testing.T) {
	fixture := seedPlanningScopeFixture(t)
	localMilestoneA := planningScopeInsertID(t, fixture.db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Local milestone A', '', 'planning', false, ?)
	`, fixture.workspaceA)
	localMilestoneB := planningScopeInsertID(t, fixture.db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Local milestone B', '', 'planning', false, ?)
	`, fixture.workspaceB)
	localIterationA := planningScopeInsertID(t, fixture.db, `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Local iteration A', '', '2026-07-01', '2026-07-31', 'active', false, ?)
	`, fixture.workspaceA)
	localIterationB := planningScopeInsertID(t, fixture.db, `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Local iteration B', '', '2026-07-01', '2026-07-31', 'active', false, ?)
	`, fixture.workspaceB)

	assertValidationField := func(t *testing.T, err error, field string) {
		t.Helper()
		var validationErr *validation.ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("error = %v, want ValidationError", err)
		}
		if validationErr.Field != field {
			t.Fatalf("validation field = %q, want %q", validationErr.Field, field)
		}
	}

	_, err := CreateItem(fixture.db, ItemCreationParams{
		WorkspaceID: fixture.workspaceA,
		Title:       "Cross-workspace milestone",
		MilestoneIDs: []int{
			localMilestoneB,
		},
	})
	assertValidationField(t, err, "milestone_ids")

	_, err = CreateItem(fixture.db, ItemCreationParams{
		WorkspaceID: fixture.workspaceA,
		Title:       "Cross-workspace iteration",
		IterationID: &localIterationB,
	})
	assertValidationField(t, err, "iteration_id")

	updateService := NewItemUpdateService(fixture.db)
	_, err = updateService.UpdateItem(UpdateItemRequest{
		ItemID: fixture.itemA,
		UpdateData: map[string]interface{}{
			"milestone_ids": []int{localMilestoneB},
		},
	})
	assertValidationField(t, err, "milestone_ids")

	_, err = updateService.UpdateItem(UpdateItemRequest{
		ItemID: fixture.itemA,
		UpdateData: map[string]interface{}{
			"iteration_id": localIterationB,
		},
	})
	assertValidationField(t, err, "iteration_id")

	if _, err := fixture.db.ExecWrite(`UPDATE items SET iteration_id = ? WHERE id = ?`, localIterationA, fixture.itemA); err != nil {
		t.Fatalf("set local iteration: %v", err)
	}
	if _, err := fixture.db.ExecWrite(`DELETE FROM item_milestones WHERE item_id = ?`, fixture.itemA); err != nil {
		t.Fatalf("clear milestone: %v", err)
	}
	if _, err := fixture.db.ExecWrite(`INSERT INTO item_milestones (item_id, milestone_id) VALUES (?, ?)`, fixture.itemA, localMilestoneA); err != nil {
		t.Fatalf("set local milestone: %v", err)
	}
	_, err = updateService.UpdateItem(UpdateItemRequest{
		ItemID: fixture.itemA,
		UpdateData: map[string]interface{}{
			"workspace_id": fixture.workspaceB,
		},
	})
	assertValidationField(t, err, "workspace_id")

	if err := validation.ValidatePlanningAssignments(fixture.db, fixture.workspaceA, []int{fixture.milestoneID, localMilestoneA}, &fixture.iterationID); err != nil {
		t.Fatalf("same-workspace and global assignments rejected: %v", err)
	}
}

func TestGenericIterationUpdateCannotBypassCompletionLifecycle(t *testing.T) {
	fixture := seedPlanningScopeFixture(t)
	service := NewPlanningService(fixture.db)
	params := UpdateIterationParams{
		ID:          fixture.iterationID,
		Name:        "Shared iteration",
		Description: "",
		StartDate:   "2026-07-01",
		EndDate:     "2026-07-31",
		Status:      "completed",
	}

	if _, err := service.UpdateIteration(params); !errors.Is(err, ErrIterationCompletionRequired) {
		t.Fatalf("generic completion error = %v, want ErrIterationCompletionRequired", err)
	}
	iteration, err := service.GetIteration(fixture.iterationID)
	if err != nil {
		t.Fatalf("GetIteration: %v", err)
	}
	if iteration.Status != "active" {
		t.Fatalf("status after rejected completion = %q, want active", iteration.Status)
	}

	if _, err := fixture.db.ExecWrite(`UPDATE iterations SET status = 'completed' WHERE id = ?`, fixture.iterationID); err != nil {
		t.Fatalf("mark completed fixture: %v", err)
	}
	params.Status = "active"
	if _, err := service.UpdateIteration(params); !errors.Is(err, ErrIterationLifecycleConflict) {
		t.Fatalf("generic reopen error = %v, want ErrIterationLifecycleConflict", err)
	}

	params.Status = "completed"
	params.Name = "Renamed completed iteration"
	updated, err := service.UpdateIteration(params)
	if err != nil {
		t.Fatalf("metadata update on completed iteration: %v", err)
	}
	if updated.Status != "completed" || updated.Name != params.Name {
		t.Fatalf("completed metadata update = %+v", updated)
	}

	if _, err := fixture.db.ExecWrite(`UPDATE iterations SET status = 'cancelled' WHERE id = ?`, fixture.iterationID); err != nil {
		t.Fatalf("mark cancelled fixture: %v", err)
	}
	params.Status = "planned"
	if _, err := service.UpdateIteration(params); !errors.Is(err, ErrIterationLifecycleConflict) {
		t.Fatalf("generic cancelled reopen error = %v, want ErrIterationLifecycleConflict", err)
	}
}

func TestResolveLinkedSCMRepositoryRejectsUnlinkedTargets(t *testing.T) {
	db := newPlanningScopeTestDB(t)
	service := NewPlanningService(db)
	workspaceID := planningScopeInsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Release workspace', 'REL', '', true, false)
	`)
	providerID := planningScopeInsertID(t, db, `
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled)
		VALUES ('release-provider', 'Release provider', 'gitea', 'pat', true)
	`)
	connectionOne := planningScopeInsertID(t, db, `
		INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
		VALUES (?, ?, true)
	`, workspaceID, providerID)
	secondWorkspaceID := planningScopeInsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Other release workspace', 'ORL', '', true, false)
	`)
	connectionTwo := planningScopeInsertID(t, db, `
		INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
		VALUES (?, ?, true)
	`, secondWorkspaceID, providerID)
	repositoryID := planningScopeInsertID(t, db, `
		INSERT INTO workspace_repositories (
			workspace_scm_connection_id, repository_external_id, repository_name, repository_url
		) VALUES (?, 'linked-repo', 'windshift/linked', 'https://example.test/windshift/linked')
	`, connectionOne)

	linked, err := service.ResolveLinkedSCMRepository(connectionOne, repositoryID, "windshift/linked")
	if err != nil {
		t.Fatalf("resolve linked repository by id: %v", err)
	}
	if linked.ID != repositoryID || linked.RepositoryName != "windshift/linked" {
		t.Fatalf("linked repository = %+v", linked)
	}
	if _, err := service.ResolveLinkedSCMRepository(connectionOne, 0, "windshift/linked"); err != nil {
		t.Fatalf("resolve linked repository by legacy name: %v", err)
	}

	for name, testCase := range map[string]struct {
		connectionID int
		repositoryID int
		name         string
	}{
		"free-form repository":               {connectionOne, 0, "windshift/unlinked"},
		"repository from another connection": {connectionTwo, repositoryID, "windshift/linked"},
		"mismatched id and name":             {connectionOne, repositoryID, "windshift/other"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.ResolveLinkedSCMRepository(testCase.connectionID, testCase.repositoryID, testCase.name)
			if !errors.Is(err, ErrSCMRepositoryNotLinked) {
				t.Fatalf("error = %v, want ErrSCMRepositoryNotLinked", err)
			}
		})
	}
}
