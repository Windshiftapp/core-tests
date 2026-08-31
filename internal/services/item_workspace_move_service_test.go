//go:build test

package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

func TestItemWorkspaceMovePreviewsAndCommitsExactPolicy(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	destinationWorkspaceID := moveInsertID(t, tdb, `INSERT INTO workspaces (name, key, active) VALUES ('Destination', 'DST', true) RETURNING id`)
	configSetID := moveQueryID(t, tdb, `SELECT id FROM configuration_sets WHERE is_default = true ORDER BY id LIMIT 1`)
	itemTypeID := moveQueryID(t, tdb, `SELECT item_type_id FROM configuration_set_item_types WHERE configuration_set_id = ? ORDER BY id LIMIT 1`, configSetID)
	statusID := moveQueryID(t, tdb, `
		SELECT wt.to_status_id
		FROM workflow_transitions wt
		JOIN configuration_sets cs ON cs.workflow_id = wt.workflow_id
		WHERE cs.id = ? AND wt.from_status_id IS NULL
		ORDER BY wt.display_order, wt.id LIMIT 1
	`, configSetID)
	if _, err := tdb.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, destinationWorkspaceID, configSetID); err != nil {
		t.Fatalf("attach destination config set: %v", err)
	}

	keptFieldID := moveInsertID(t, tdb, `INSERT INTO custom_field_definitions (name, field_type) VALUES ('Kept field', 'text') RETURNING id`)
	droppedFieldID := moveInsertID(t, tdb, `INSERT INTO custom_field_definitions (name, field_type) VALUES ('Dropped field', 'text') RETURNING id`)
	screenID := moveInsertID(t, tdb, `INSERT INTO screens (name) VALUES ('Move target create') RETURNING id`)
	if _, err := tdb.Exec(`INSERT INTO screen_fields (screen_id, field_type, field_identifier) VALUES (?, 'custom', ?)`, screenID, fmt.Sprint(keptFieldID)); err != nil {
		t.Fatalf("add destination custom field: %v", err)
	}
	if _, err := tdb.Exec(`UPDATE configuration_set_item_types SET create_screen_id = ? WHERE configuration_set_id = ? AND item_type_id = ?`, screenID, configSetID, itemTypeID); err != nil {
		t.Fatalf("set destination create screen: %v", err)
	}

	customJSON, _ := json.Marshal(map[string]interface{}{
		fmt.Sprint(keptFieldID):    "keep me",
		fmt.Sprint(droppedFieldID): "drop me",
	})
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:           seed.WorkspaceID,
		ItemTypeID:            &itemTypeID,
		Title:                 "Move me",
		StatusID:              &statusID,
		PriorityID:            &seed.PriorityID,
		CustomFieldValuesJSON: string(customJSON),
	})
	if err != nil {
		t.Fatalf("insert root item: %v", err)
	}
	itemID := int(itemID64)
	createMoveChild := func(title string, parentID *int) int {
		t.Helper()
		id, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: seed.WorkspaceID,
			ItemTypeID:  &itemTypeID,
			Title:       title,
			StatusID:    &statusID,
			ParentID:    parentID,
		})
		if err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
		return int(id)
	}
	childID := createMoveChild("Child", &itemID)
	grandchildID := createMoveChild("Grandchild", &childID)
	destinationID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: destinationWorkspaceID,
		ItemTypeID:  &itemTypeID,
		Title:       "Destination existing",
		StatusID:    &statusID,
	})
	if err != nil {
		t.Fatalf("insert destination item: %v", err)
	}
	// Pin the fixture's workspace item numbers and stored paths after all
	// creates: CreateItem allocates numbers via MAX+1, so interleaving a pin
	// would bump the sequence for later creates.
	for _, pin := range []struct {
		id     int
		number int
	}{
		{itemID, 50},
		{int(destinationID64), 8},
	} {
		if _, err := tdb.Exec(`UPDATE items SET workspace_item_number = ? WHERE id = ?`, pin.number, pin.id); err != nil {
			t.Fatalf("pin fixture item number: %v", err)
		}
	}
	// The detach assertions below depend on the pre-move stored paths; the
	// production create path leaves path at its default, so write the fixture
	// paths after creation.
	for _, pathFix := range []struct {
		id   int
		path string
	}{
		{childID, fmt.Sprintf("/%d/", itemID)},
		{grandchildID, fmt.Sprintf("/%d/%d/", itemID, childID)},
	} {
		if _, err := tdb.Exec(`UPDATE items SET path = ? WHERE id = ?`, pathFix.path, pathFix.id); err != nil {
			t.Fatalf("pin fixture path: %v", err)
		}
	}

	sourceKeepLabelID := moveInsertID(t, tdb, `INSERT INTO labels (name) VALUES ('Keep') RETURNING id`)
	sourceDropLabelID := moveInsertID(t, tdb, `INSERT INTO labels (name) VALUES ('Drop') RETURNING id`)
	for _, labelID := range []int{sourceKeepLabelID, sourceDropLabelID} {
		if _, err := tdb.Exec(`INSERT INTO item_labels (item_id, label_id) VALUES (?, ?)`, itemID, labelID); err != nil {
			t.Fatalf("attach source label: %v", err)
		}
	}
	milestoneID := moveInsertID(t, tdb, `INSERT INTO milestones (name, is_global) VALUES ('Source milestone', true) RETURNING id`)
	if _, err := tdb.Exec(`INSERT INTO item_milestones (item_id, milestone_id) VALUES (?, ?)`, itemID, milestoneID); err != nil {
		t.Fatalf("attach milestone: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO comments (item_id, author_id, content) VALUES (?, ?, 'travels with item')`, itemID, seed.UserID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	itemRepo := repository.NewItemRepository(db)
	workspaceTotal := func(workspaceID int) int {
		t.Helper()
		_, total, err := itemRepo.FindAllWithDetailsContext(t.Context(), repository.ItemListParams{
			WorkspaceIDs: []int{workspaceID},
			Filters:      repository.ItemFilters{WorkspaceID: &workspaceID},
		})
		if err != nil {
			t.Fatalf("list workspace %d items: %v", workspaceID, err)
		}
		return total
	}
	sourceTotalBefore := workspaceTotal(seed.WorkspaceID)
	destinationTotalBefore := workspaceTotal(destinationWorkspaceID)

	service := NewItemWorkspaceMoveService(db)
	preview, err := service.Preview(itemID, ItemWorkspaceMoveInput{DestinationWorkspaceID: destinationWorkspaceID})
	if err != nil {
		t.Fatalf("preview move: %v", err)
	}
	if preview.SourceKey != "TEST-50" || preview.DestinationWorkspaceKey != "DST" {
		t.Fatalf("preview keys = %q -> %q", preview.SourceKey, preview.DestinationWorkspaceKey)
	}
	if preview.ChildrenDetached != 1 || fmt.Sprint(preview.LabelsKept) != "[Drop Keep]" || len(preview.LabelsDropped) != 0 {
		t.Fatalf("preview hierarchy/labels = children %d kept %v dropped %v", preview.ChildrenDetached, preview.LabelsKept, preview.LabelsDropped)
	}
	if fmt.Sprint(preview.CustomFieldsKept) != "[Kept field]" || fmt.Sprint(preview.CustomFieldsDropped) != "[Dropped field]" {
		t.Fatalf("preview custom fields = kept %v dropped %v", preview.CustomFieldsKept, preview.CustomFieldsDropped)
	}

	result, err := service.Move(itemID, seed.UserID, ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationWorkspaceID,
		TargetItemTypeID:       preview.TargetItemTypeID,
		TargetStatusID:         preview.TargetStatusID,
		TargetPriorityID:       preview.TargetPriorityID,
	})
	if err != nil {
		t.Fatalf("move item: %v", err)
	}
	if result.OldKey != "TEST-50" || result.NewKey != "DST-9" || result.Item.WorkspaceID != destinationWorkspaceID {
		t.Fatalf("move result = %+v", result)
	}
	if sourceTotalAfter := workspaceTotal(seed.WorkspaceID); sourceTotalAfter != sourceTotalBefore-1 {
		t.Fatalf("source total after move = %d, want %d", sourceTotalAfter, sourceTotalBefore-1)
	}
	if destinationTotalAfter := workspaceTotal(destinationWorkspaceID); destinationTotalAfter != destinationTotalBefore+1 {
		t.Fatalf("destination total after move = %d, want %d", destinationTotalAfter, destinationTotalBefore+1)
	}

	if _, err := itemRepo.FindIDByKeyAndNumber("TEST", 50); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("old key lookup error = %v, want not found", err)
	}
	resolvedID, err := itemRepo.FindIDByKeyAndNumber("DST", 9)
	if err != nil || resolvedID != itemID {
		t.Fatalf("new key resolves to %d, %v; want %d", resolvedID, err, itemID)
	}

	var childParent *int
	var childPath, grandchildPath string
	if err := tdb.QueryRow(`SELECT parent_id, path FROM items WHERE id = ?`, childID).Scan(&childParent, &childPath); err != nil {
		t.Fatalf("load detached child: %v", err)
	}
	if err := tdb.QueryRow(`SELECT path FROM items WHERE id = ?`, grandchildID).Scan(&grandchildPath); err != nil {
		t.Fatalf("load grandchild: %v", err)
	}
	if childParent != nil || childPath != "/" || grandchildPath != fmt.Sprintf("/%d/", childID) {
		t.Fatalf("detached hierarchy = parent %v child path %q grandchild path %q", childParent, childPath, grandchildPath)
	}

	var storedCustomJSON string
	var iterationID, projectID, timeProjectID, channelID, requestTypeID *int
	if err := tdb.QueryRow(`SELECT custom_field_values, iteration_id, project_id, time_project_id, channel_id, request_type_id FROM items WHERE id = ?`, itemID).Scan(&storedCustomJSON, &iterationID, &projectID, &timeProjectID, &channelID, &requestTypeID); err != nil {
		t.Fatalf("load moved fields: %v", err)
	}
	var storedCustom map[string]interface{}
	if err := json.Unmarshal([]byte(storedCustomJSON), &storedCustom); err != nil {
		t.Fatalf("decode moved custom fields: %v", err)
	}
	if len(storedCustom) != 1 || storedCustom[fmt.Sprint(keptFieldID)] != "keep me" {
		t.Fatalf("stored custom fields = %#v", storedCustom)
	}
	if iterationID != nil || projectID != nil || timeProjectID != nil || channelID != nil || requestTypeID != nil {
		t.Fatalf("workspace-scoped fields were not cleared")
	}

	var labelCount, milestoneCount, commentCount, historyCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM item_labels WHERE item_id = ? AND label_id IN (?, ?)`, itemID, sourceKeepLabelID, sourceDropLabelID).Scan(&labelCount); err != nil || labelCount != 2 {
		t.Fatalf("preserved global labels = %d, %v; want 2", labelCount, err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM item_milestones WHERE item_id = ?`, itemID).Scan(&milestoneCount); err != nil || milestoneCount != 0 {
		t.Fatalf("milestone count = %d, %v", milestoneCount, err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, itemID).Scan(&commentCount); err != nil || commentCount != 1 {
		t.Fatalf("comment count = %d, %v", commentCount, err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM item_history WHERE item_id = ? AND field_name = 'workspace_move' AND new_value LIKE '%DST-9%'`, itemID).Scan(&historyCount); err != nil || historyCount != 1 {
		t.Fatalf("move history count = %d, %v", historyCount, err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin allocation check: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	next, err := itemRepo.GetNextWorkspaceItemNumber(tx, seed.WorkspaceID)
	if err != nil || next != 51 {
		t.Fatalf("source next number = %d, %v; want 51", next, err)
	}
}

func TestItemWorkspaceMoveRejectsPendingApproval(t *testing.T) {
	env := newApprovalTestEnv(t)
	if _, err := env.db.Exec(`UPDATE statuses SET description = '' WHERE id IN (?, ?, ?, ?)`, env.statusOpenID, env.statusReviewID, env.statusApprovedID, env.statusRejectedID); err != nil {
		t.Fatalf("normalize status descriptions: %v", err)
	}
	env.createApprovalSet(approvalSetSpec{
		steps: []approvalStepSpec{userStep(0, "Required", env.approver1)},
	})
	request, err := env.approvalService.RequestApproval(t.Context(), env.itemID, env.statusReviewID, env.statusOpenID, env.requestor)
	if err != nil {
		t.Fatalf("request approval: %v", err)
	}

	var destinationWorkspaceID int
	if err := env.db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Destination', 'DST', true) RETURNING id`).Scan(&destinationWorkspaceID); err != nil {
		t.Fatalf("create destination workspace: %v", err)
	}
	if _, err := env.db.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, destinationWorkspaceID, env.configurationSetID); err != nil {
		t.Fatalf("attach destination config set: %v", err)
	}

	service := NewItemWorkspaceMoveService(env.db)
	_, err = service.Move(env.itemID, env.requestor, ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationWorkspaceID,
		TargetItemTypeID:       env.itemTypeID,
		TargetStatusID:         env.statusReviewID,
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "destination_workspace_id" {
		t.Fatalf("Move error = %v, want destination_workspace_id validation error", err)
	}

	var workspaceID int
	if err := env.db.QueryRow(`SELECT workspace_id FROM items WHERE id = ?`, env.itemID).Scan(&workspaceID); err != nil {
		t.Fatalf("load item workspace: %v", err)
	}
	if workspaceID != env.workspaceID {
		t.Fatalf("workspace_id = %d, want source %d", workspaceID, env.workspaceID)
	}
	var requestStatus string
	if err := env.db.QueryRow(`SELECT status FROM approval_requests WHERE id = ?`, request.ID).Scan(&requestStatus); err != nil {
		t.Fatalf("load approval request: %v", err)
	}
	if requestStatus != "pending" {
		t.Fatalf("approval request status = %q, want pending", requestStatus)
	}
}

func TestItemWorkspaceMoveRejectsConditionGatedStatus(t *testing.T) {
	env := newApprovalTestEnv(t)
	if _, err := env.db.Exec(`UPDATE statuses SET description = '' WHERE id IN (?, ?, ?, ?)`, env.statusOpenID, env.statusReviewID, env.statusApprovedID, env.statusRejectedID); err != nil {
		t.Fatalf("normalize status descriptions: %v", err)
	}
	var destinationWorkspaceID int
	if err := env.db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Condition destination', 'COND', true) RETURNING id`).Scan(&destinationWorkspaceID); err != nil {
		t.Fatalf("create destination workspace: %v", err)
	}
	var conditionSetID int
	if err := env.db.QueryRow(`INSERT INTO condition_sets (name, workflow_id) VALUES ('Move conditions', ?) RETURNING id`, env.workflowID).Scan(&conditionSetID); err != nil {
		t.Fatalf("create condition set: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE configuration_sets SET condition_set_id = ? WHERE id = ?`, conditionSetID, env.configurationSetID); err != nil {
		t.Fatalf("attach condition set: %v", err)
	}
	if _, err := env.db.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, destinationWorkspaceID, env.configurationSetID); err != nil {
		t.Fatalf("attach destination config set: %v", err)
	}
	var conditionTransitionID int
	if err := env.db.QueryRow(`INSERT INTO condition_set_transitions (condition_set_id, transition_id) VALUES (?, ?) RETURNING id`, conditionSetID, env.transitionReviewToApproved).Scan(&conditionTransitionID); err != nil {
		t.Fatalf("create condition transition: %v", err)
	}
	if _, err := env.db.Exec(`INSERT INTO conditions (condition_set_transition_id, condition_type, config) VALUES (?, 'field_value', '{}')`, conditionTransitionID); err != nil {
		t.Fatalf("create transition condition: %v", err)
	}

	service := NewItemWorkspaceMoveService(env.db)
	_, err := service.Move(env.itemID, env.requestor, ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationWorkspaceID,
		TargetItemTypeID:       env.itemTypeID,
		TargetStatusID:         env.statusApprovedID,
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "target_status_id" {
		t.Fatalf("Move error = %v, want target_status_id validation error", err)
	}

	var workspaceID, statusID int
	if err := env.db.QueryRow(`SELECT workspace_id, status_id FROM items WHERE id = ?`, env.itemID).Scan(&workspaceID, &statusID); err != nil {
		t.Fatalf("load item: %v", err)
	}
	if workspaceID != env.workspaceID || statusID != env.statusReviewID {
		t.Fatalf("item moved to workspace/status %d/%d, want %d/%d", workspaceID, statusID, env.workspaceID, env.statusReviewID)
	}
}

func TestItemWorkspaceMoveRejectsApprovalBoundStatus(t *testing.T) {
	env := newApprovalTestEnv(t)
	if _, err := env.db.Exec(`UPDATE statuses SET description = '' WHERE id IN (?, ?, ?, ?)`, env.statusOpenID, env.statusReviewID, env.statusApprovedID, env.statusRejectedID); err != nil {
		t.Fatalf("normalize status descriptions: %v", err)
	}
	destinationWorkspaceID := moveWorkspaceForApprovalTests(t, env, "Approval destination", "APPR")
	approvalSetID := approvalMoveInsertID(t, env, `INSERT INTO approval_sets (name, workflow_id) VALUES ('Move approval set', ?) RETURNING id`, env.workflowID)
	if _, err := env.db.Exec(`
		INSERT INTO approval_set_statuses
			(approval_set_id, status_id, approve_transition_id, deny_transition_id)
		VALUES (?, ?, ?, ?)
	`, approvalSetID, env.statusApprovedID, env.transitionReviewToApproved, env.transitionReviewToRejected); err != nil {
		t.Fatalf("bind approval status: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE configuration_sets SET approval_set_id = ? WHERE id = ?`, approvalSetID, env.configurationSetID); err != nil {
		t.Fatalf("attach approval set: %v", err)
	}

	_, err := NewItemWorkspaceMoveService(env.db).Move(env.itemID, env.requestor, ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationWorkspaceID,
		TargetItemTypeID:       env.itemTypeID,
		TargetStatusID:         env.statusApprovedID,
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "target_status_id" {
		t.Fatalf("Move error = %v, want target_status_id validation error", err)
	}
}

func TestItemWorkspaceMovePreservesCompletedApprovalAudit(t *testing.T) {
	env := newApprovalTestEnv(t)
	if _, err := env.db.Exec(`UPDATE statuses SET description = '' WHERE id IN (?, ?, ?, ?)`, env.statusOpenID, env.statusReviewID, env.statusApprovedID, env.statusRejectedID); err != nil {
		t.Fatalf("normalize status descriptions: %v", err)
	}
	env.createApprovalSet(approvalSetSpec{
		steps: []approvalStepSpec{userStep(0, "Required", env.approver1)},
	})
	request, err := env.approvalService.RequestApproval(t.Context(), env.itemID, env.statusReviewID, env.statusOpenID, env.requestor)
	if err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, completed, err := env.approvalService.Decide(t.Context(), request.ID, env.approver1, models.ApprovalDecisionApprove, "approved", DecideOptions{}); err != nil || completed.Status != models.ApprovalRequestStatusApproved {
		t.Fatalf("complete approval = %v, %v", completed, err)
	}
	destinationWorkspaceID := moveWorkspaceForApprovalTests(t, env, "Audit destination", "AUDIT")

	if _, err := NewItemWorkspaceMoveService(env.db).Move(env.itemID, env.requestor, ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationWorkspaceID,
		TargetItemTypeID:       env.itemTypeID,
		TargetStatusID:         env.statusApprovedID,
	}); err != nil {
		t.Fatalf("move item: %v", err)
	}
	var requestStatus string
	if err := env.db.QueryRow(`SELECT status FROM approval_requests WHERE id = ?`, request.ID).Scan(&requestStatus); err != nil {
		t.Fatalf("load completed approval after move: %v", err)
	}
	if requestStatus != models.ApprovalRequestStatusApproved {
		t.Fatalf("approval request status = %q, want approved", requestStatus)
	}
}

func moveWorkspaceForApprovalTests(t *testing.T, env *approvalTestEnv, name, key string) int {
	t.Helper()
	workspaceID := approvalMoveInsertID(t, env, `INSERT INTO workspaces (name, key, active) VALUES (?, ?, true) RETURNING id`, name, key)
	if _, err := env.db.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, env.configurationSetID); err != nil {
		t.Fatalf("attach destination config set: %v", err)
	}
	return workspaceID
}

func approvalMoveInsertID(t *testing.T, env *approvalTestEnv, query string, args ...any) int {
	t.Helper()
	var id int
	if err := env.db.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
	return id
}

func moveInsertID(t *testing.T, tdb *testutils.TestDB, query string, args ...interface{}) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatalf("insert id: %v", err)
	}
	return id
}

func moveQueryID(t *testing.T, tdb *testutils.TestDB, query string, args ...interface{}) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatalf("query id: %v", err)
	}
	return id
}
