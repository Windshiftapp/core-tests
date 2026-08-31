//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type reconciliationTestEnv struct {
	db          database.Database
	workspaceID int
	userID      int
	statusID    int
	priorityID  int
	itemTypeID  int
	milestoneID int
	labelID     int
}

func newReconciliationTestEnv(t *testing.T) reconciliationTestEnv {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var itemTypeID int
	if err := db.QueryRow("SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("resolve default item type: %v", err)
	}
	milestone, err := NewPlanningService(db).CreateMilestone(CreateMilestoneParams{
		Name:        "External reconciliation milestone",
		Status:      "planning",
		WorkspaceID: &data.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	labelID, _, err := repository.NewLabelRepository(db).Create("external-reconciliation", "336699")
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	return reconciliationTestEnv{
		db:          db,
		workspaceID: data.WorkspaceID,
		userID:      data.UserID,
		statusID:    data.StatusID,
		priorityID:  data.PriorityID,
		itemTypeID:  itemTypeID,
		milestoneID: milestone.ID,
		labelID:     int(labelID),
	}
}

func TestExternalItemReconciliationMatchesCanonicalCreateAndUpdateContracts(t *testing.T) {
	env := newReconciliationTestEnv(t)
	input := ItemCreateInput{
		WorkspaceID:  env.workspaceID,
		Title:        "  Shared contract title  ",
		Description:  "Shared contract description",
		StatusID:     &env.statusID,
		PriorityID:   &env.priorityID,
		ItemTypeID:   &env.itemTypeID,
		MilestoneIDs: []int{env.milestoneID},
	}

	canonical, err := NewItemCreationService(env.db, nil).Create(env.userID, "testuser", input)
	if err != nil {
		t.Fatalf("canonical create: %v", err)
	}
	reconciled, err := NewExternalItemReconciliationService(env.db).Create(t.Context(), ExternalItemCreateRequest{
		Policy: GitHubIssueSyncReconciliationPolicy(),
		Input:  input,
	})
	if err != nil {
		t.Fatalf("reconciled create: %v", err)
	}
	assertReconciliationContract(t, canonical.Item, reconciled)

	nextMilestone, err := NewPlanningService(env.db).CreateMilestone(CreateMilestoneParams{
		Name:        "Updated shared contract milestone",
		Status:      "planning",
		WorkspaceID: &env.workspaceID,
	})
	if err != nil {
		t.Fatalf("create next milestone: %v", err)
	}
	updateData := map[string]any{
		"title":         "  Updated shared contract  ",
		"description":   "Updated shared description",
		"milestone_ids": []int{nextMilestone.ID},
	}
	canonicalUpdate, err := NewItemUpdateService(env.db).UpdateItem(UpdateItemRequest{
		ItemID:     canonical.Item.ID,
		UpdateData: updateData,
		UserID:     env.userID,
	})
	if err != nil {
		t.Fatalf("canonical update: %v", err)
	}
	reconciliationUpdateData := map[string]any{
		"title":         updateData["title"],
		"description":   updateData["description"],
		"milestone_ids": updateData["milestone_ids"],
		"status_id":     env.statusID,
	}
	reconciledUpdate, err := NewExternalItemReconciliationService(env.db).Update(t.Context(), ExternalItemUpdateRequest{
		Policy:     GitHubIssueSyncReconciliationPolicy(),
		ItemID:     reconciled.ID,
		UpdateData: reconciliationUpdateData,
	})
	if err != nil {
		t.Fatalf("reconciled update: %v", err)
	}
	assertReconciliationContract(t, canonicalUpdate.Item, reconciledUpdate.Item)
}

func assertReconciliationContract(t *testing.T, canonical, reconciled *models.Item) {
	t.Helper()
	if canonical.Title != reconciled.Title ||
		canonical.Description != reconciled.Description ||
		!equalIntPointers(canonical.StatusID, reconciled.StatusID) ||
		!equalIntPointers(canonical.PriorityID, reconciled.PriorityID) ||
		!equalIntPointers(canonical.ItemTypeID, reconciled.ItemTypeID) {
		t.Fatalf("canonical item = %+v, reconciled item = %+v", canonical, reconciled)
	}
	if len(canonical.Milestones) != len(reconciled.Milestones) {
		t.Fatalf("canonical milestones = %+v, reconciled milestones = %+v", canonical.Milestones, reconciled.Milestones)
	}
	for i := range canonical.Milestones {
		if canonical.Milestones[i].ID != reconciled.Milestones[i].ID {
			t.Fatalf("canonical milestones = %+v, reconciled milestones = %+v", canonical.Milestones, reconciled.Milestones)
		}
	}
}

func equalIntPointers(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func TestExternalItemReconciliationCreateUsesCanonicalMutationAndSideEffects(t *testing.T) {
	env := newReconciliationTestEnv(t)
	spy := installItemChangeSpy(t)

	item, err := NewExternalItemReconciliationService(env.db).Create(t.Context(), ExternalItemCreateRequest{
		Policy: GitHubIssueSyncReconciliationPolicy(),
		Input: ItemCreateInput{
			WorkspaceID:  env.workspaceID,
			Title:        "  Imported issue  ",
			Description:  "GitHub body",
			StatusID:     &env.statusID,
			PriorityID:   &env.priorityID,
			MilestoneIDs: []int{env.milestoneID},
		},
		AfterCreate: func(ctx context.Context, tx database.Tx, itemID int) error {
			return repository.NewLabelRepository(env.db).ReplaceItemLabelsTx(ctx, tx, itemID, []int{env.labelID})
		},
	})
	if err != nil {
		t.Fatalf("create reconciled item: %v", err)
	}
	if item.Title != "Imported issue" || item.Description != "GitHub body" {
		t.Fatalf("reconciled item = title %q description %q", item.Title, item.Description)
	}
	if item.CreatorID != nil {
		t.Fatalf("creator = %v, want source provenance without user attribution", item.CreatorID)
	}
	if item.ItemTypeID == nil || *item.ItemTypeID != env.itemTypeID {
		t.Fatalf("item type = %v, want canonical default %d", item.ItemTypeID, env.itemTypeID)
	}
	if len(item.Milestones) != 1 || item.Milestones[0].ID != env.milestoneID {
		t.Fatalf("milestones = %+v, want %d", item.Milestones, env.milestoneID)
	}
	labels, err := repository.NewLabelRepository(env.db).ListForItem(item.ID)
	if err != nil {
		t.Fatalf("list item labels: %v", err)
	}
	if len(labels) != 1 || labels[0].ID != env.labelID {
		t.Fatalf("labels = %+v, want %d", labels, env.labelID)
	}
	var historyCount int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM item_history WHERE item_id = ?", item.ID).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyCount != 0 {
		t.Fatalf("history count = %d, want no user-attributed history", historyCount)
	}
	live := spy.snapshot()
	if len(live) != 1 || live[0].itemID != item.ID || live[0].kind != ItemChangeCreated {
		t.Fatalf("live updates = %+v, want one item.created for %d", live, item.ID)
	}
}

func TestExternalItemReconciliationCreateHookFailureRollsBackItemMilestoneAndLabel(t *testing.T) {
	env := newReconciliationTestEnv(t)
	wantErr := errors.New("source tracking failed")

	_, err := NewExternalItemReconciliationService(env.db).Create(t.Context(), ExternalItemCreateRequest{
		Policy: GitHubIssueSyncReconciliationPolicy(),
		Input: ItemCreateInput{
			WorkspaceID:  env.workspaceID,
			Title:        "Rollback imported issue",
			StatusID:     &env.statusID,
			PriorityID:   &env.priorityID,
			ItemTypeID:   &env.itemTypeID,
			MilestoneIDs: []int{env.milestoneID},
		},
		AfterCreate: func(ctx context.Context, tx database.Tx, itemID int) error {
			if err := repository.NewLabelRepository(env.db).ReplaceItemLabelsTx(ctx, tx, itemID, []int{env.labelID}); err != nil {
				return err
			}
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("create error = %v, want %v", err, wantErr)
	}

	var itemCount, milestoneCount, labelCount int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM items WHERE title = ?", "Rollback imported issue").Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if err := env.db.QueryRow("SELECT COUNT(*) FROM item_milestones").Scan(&milestoneCount); err != nil {
		t.Fatalf("count item milestones: %v", err)
	}
	if err := env.db.QueryRow("SELECT COUNT(*) FROM item_labels").Scan(&labelCount); err != nil {
		t.Fatalf("count item labels: %v", err)
	}
	if itemCount != 0 || milestoneCount != 0 || labelCount != 0 {
		t.Fatalf("rollback counts = items %d milestones %d labels %d, want all zero", itemCount, milestoneCount, labelCount)
	}
}

func TestExternalItemReconciliationUpdateUsesCanonicalMutationWithoutUserHistory(t *testing.T) {
	env := newReconciliationTestEnv(t)
	oldMilestone, err := NewPlanningService(env.db).CreateMilestone(CreateMilestoneParams{
		Name:        "Old reconciliation milestone",
		Status:      "planning",
		WorkspaceID: &env.workspaceID,
	})
	if err != nil {
		t.Fatalf("create old milestone: %v", err)
	}
	createdID, err := CreateItem(env.db, ItemCreationParams{
		WorkspaceID:  env.workspaceID,
		Title:        "Original title",
		StatusID:     &env.statusID,
		PriorityID:   &env.priorityID,
		ItemTypeID:   &env.itemTypeID,
		MilestoneIDs: []int{oldMilestone.ID},
	})
	if err != nil {
		t.Fatalf("create original item: %v", err)
	}
	itemID := int(createdID)
	spy := installItemChangeSpy(t)

	result, err := NewExternalItemReconciliationService(env.db).Update(t.Context(), ExternalItemUpdateRequest{
		Policy: GitHubIssueSyncReconciliationPolicy(),
		ItemID: itemID,
		UpdateData: map[string]any{
			"title":         "  Updated from GitHub  ",
			"status_id":     env.statusID,
			"milestone_ids": []int{env.milestoneID},
		},
		AfterUpdate: func(ctx context.Context, tx database.Tx, _, _ *models.Item) error {
			return repository.NewLabelRepository(env.db).ReplaceItemLabelsTx(ctx, tx, itemID, []int{env.labelID})
		},
	})
	if err != nil {
		t.Fatalf("update reconciled item: %v", err)
	}
	if result.Item.Title != "Updated from GitHub" {
		t.Fatalf("title = %q, want canonical title", result.Item.Title)
	}
	if len(result.Item.Milestones) != 1 || result.Item.Milestones[0].ID != env.milestoneID {
		t.Fatalf("milestones = %+v, want %d", result.Item.Milestones, env.milestoneID)
	}
	if len(result.FieldChanges) != 0 {
		t.Fatalf("field changes = %+v, want no user history", result.FieldChanges)
	}
	var historyCount int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM item_history WHERE item_id = ?", itemID).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyCount != 0 {
		t.Fatalf("history count = %d, want zero", historyCount)
	}
	live := spy.snapshot()
	if len(live) != 1 || live[0].itemID != itemID || live[0].kind != ItemChangeUpdated {
		t.Fatalf("live updates = %+v, want one item.updated for %d", live, itemID)
	}
}

func TestExternalItemReconciliationRejectsStatusOutsideWorkflowBeforeMutation(t *testing.T) {
	env := newReconciliationTestEnv(t)
	createdID, err := CreateItem(env.db, ItemCreationParams{
		WorkspaceID: env.workspaceID,
		Title:       "Unchanged",
		StatusID:    &env.statusID,
		ItemTypeID:  &env.itemTypeID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID := int(createdID)
	hookCalled := false

	_, err = NewExternalItemReconciliationService(env.db).Update(t.Context(), ExternalItemUpdateRequest{
		Policy:     GitHubIssueSyncReconciliationPolicy(),
		ItemID:     itemID,
		UpdateData: map[string]any{"title": "Must roll back", "status_id": 999999},
		AfterUpdate: func(context.Context, database.Tx, *models.Item, *models.Item) error {
			hookCalled = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("update error = nil, want workflow status validation error")
	}
	if hookCalled {
		t.Fatal("source hook ran for an invalid workflow status")
	}
	var title string
	if err := env.db.QueryRow("SELECT title FROM items WHERE id = ?", itemID).Scan(&title); err != nil {
		t.Fatalf("read item title: %v", err)
	}
	if title != "Unchanged" {
		t.Fatalf("title = %q, want unchanged", title)
	}
}
