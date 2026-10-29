//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// Regression for GH #267: condition nodes must be able to aggregate over
// children so an automation can transition a parent once every child is
// completed. open_child_count counts direct children; open_descendant_count
// counts descendants at any depth; both exclude completed statuses.
func TestActionConditionOpenChildCounts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	// Fixture: a completed status category + status. Direct SQL because the
	// status catalog is infrastructure, not creatable through the item API.
	var completedStatusID int
	var completedCategoryID int
	err := db.QueryRow(`
		INSERT INTO status_categories (name, color, is_default, is_completed)
		VALUES ('Done Category', '#22c55e', false, true) RETURNING id
	`).Scan(&completedCategoryID)
	if err != nil {
		t.Fatalf("seed completed category: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO statuses (name, category_id, is_default)
		VALUES ('Done (GH267)', ?, false) RETURNING id
	`, completedCategoryID).Scan(&completedStatusID); err != nil {
		t.Fatalf("seed completed status: %v", err)
	}

	itemRepo := repository.NewItemRepository(db)
	createItem := func(title string, parentID, statusID *int) int {
		t.Helper()
		id, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: data.WorkspaceID, Title: title,
			StatusID: statusID, CreatorID: &data.UserID, ParentID: parentID,
		})
		if err != nil {
			t.Fatalf("create item %s: %v", title, err)
		}
		return int(id)
	}

	parent := createItem("parent", nil, &data.StatusID)
	doneChild := createItem("done child", &parent, &completedStatusID)
	openChild := createItem("open child", &parent, &data.StatusID)
	createItem("open grandchild", &openChild, &data.StatusID)
	_ = doneChild

	tests := []struct {
		name      string
		itemID    int
		field     string
		want      int
		wantFound bool
	}{
		{"direct children only counts the open child", parent, "open_child_count", 1, true},
		{"descendants count child and grandchild", parent, "open_descendant_count", 2, true},
		{"completed leaf has no open children", doneChild, "open_child_count", 0, true},
		{"open child with open grandchild", openChild, "open_descendant_count", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &models.ExecutionContext{Event: &models.ActionEvent{ItemID: tt.itemID}}
			got, found := currentItemFieldValueResolved(itemRepo, ctx, tt.field)
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if got != tt.want {
				t.Fatalf("%s = %v, want %d", tt.field, got, tt.want)
			}
		})
	}

	// The "all children done" expression: count eq 0.
	condition := &models.ActionNode{
		NodeType:   models.ActionNodeCondition,
		NodeConfig: `{"field_name": "open_child_count", "operator": "eq", "value": "0"}`,
	}
	service := &ActionService{itemRepo: repository.NewItemRepository(db)}
	ctx := &models.ExecutionContext{Event: &models.ActionEvent{ItemID: parent}}
	step := &models.StepResult{}
	if err := service.executeCondition(condition, ctx, step); err != nil {
		t.Fatalf("executeCondition: %v", err)
	}
	if step.Output["condition_result"] != false {
		t.Fatalf("parent with an open child evaluated condition_result = %v, want false", step.Output["condition_result"])
	}

	if _, err := db.Exec(`UPDATE items SET status_id = ? WHERE id = ?`, completedStatusID, openChild); err != nil {
		t.Fatalf("complete open child: %v", err)
	}
	step = &models.StepResult{}
	if err := service.executeCondition(condition, ctx, step); err != nil {
		t.Fatalf("executeCondition after completion: %v", err)
	}
	if step.Output["condition_result"] != true {
		t.Fatalf("parent with all children done evaluated condition_result = %v, want true", step.Output["condition_result"])
	}
}

// Missing items and unknown ids must not fabricate a zero count.
func TestActionConditionOpenChildCountUnknownItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	_ = tdb.SeedTestData(t)

	itemRepo := repository.NewItemRepository(tdb.GetDatabase())
	ctx := &models.ExecutionContext{Event: &models.ActionEvent{ItemID: 999999}}
	if _, found := currentItemFieldValueResolved(itemRepo, ctx, "open_child_count"); found {
		t.Fatal("unknown item reported a count")
	}
}
