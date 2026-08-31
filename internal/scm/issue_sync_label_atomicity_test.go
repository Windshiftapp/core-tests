//go:build test

package scm

import (
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func createIssueSyncConfig(t *testing.T, db database.Database, workspaceID int) int {
	t.Helper()

	var providerID int
	if err := db.QueryRow(`
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled)
		VALUES (?, ?, 'github', 'pat', true) RETURNING id
	`, "issue-sync-atomicity", "Issue Sync Atomicity").Scan(&providerID); err != nil {
		t.Fatalf("create SCM provider: %v", err)
	}
	var connectionID int
	if err := db.QueryRow(`
		INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
		VALUES (?, ?, true) RETURNING id
	`, workspaceID, providerID).Scan(&connectionID); err != nil {
		t.Fatalf("create SCM connection: %v", err)
	}
	var repositoryID int
	if err := db.QueryRow(`
		INSERT INTO workspace_repositories (
			workspace_scm_connection_id, repository_external_id, repository_name, repository_url
		) VALUES (?, 'atomicity', 'windshift/atomicity', 'https://example.com/windshift/atomicity') RETURNING id
	`, connectionID).Scan(&repositoryID); err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}
	var configID int
	if err := db.QueryRow(`
		INSERT INTO issue_sync_configs (workspace_repository_id, sync_enabled)
		VALUES (?, true) RETURNING id
	`, repositoryID).Scan(&configID); err != nil {
		t.Fatalf("create issue sync config: %v", err)
	}
	return configID
}

func createIssueSyncTracking(t *testing.T, db database.Database, workspaceID, itemID int) int {
	t.Helper()
	configID := createIssueSyncConfig(t, db, workspaceID)

	var syncItemID int
	if err := db.QueryRow(`
		INSERT INTO issue_sync_items (
			issue_sync_config_id, item_id, github_issue_number, github_issue_id, github_issue_url
		) VALUES (?, ?, 1, 101, 'https://example.com/windshift/atomicity/issues/1') RETURNING id
	`, configID, itemID).Scan(&syncItemID); err != nil {
		t.Fatalf("create issue sync item: %v", err)
	}
	return syncItemID
}

func TestIssueSyncCreateLabelFailureRollsBackItemMilestoneAndTracking(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	configID := createIssueSyncConfig(t, db, data.WorkspaceID)

	var itemTypeID int
	if err := db.QueryRow("SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("resolve default item type: %v", err)
	}
	milestone, err := services.NewPlanningService(db).CreateMilestone(services.CreateMilestoneParams{
		Name:        "Issue sync rollback milestone",
		Status:      "planning",
		WorkspaceID: &data.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}

	err = NewIssueSyncService(db, nil).createItemFromIssue(t.Context(), &models.IssueSyncConfig{
		ID:                configID,
		WorkspaceID:       data.WorkspaceID,
		StatusMapping:     fmt.Sprintf(`{"open":%d}`, data.StatusID),
		MilestoneMappings: fmt.Sprintf(`{"7":%d}`, milestone.ID),
		DefaultItemTypeID: &itemTypeID,
		DefaultPriorityID: &data.PriorityID,
		LabelSyncMode:     models.IssueSyncLabelMapped,
		LabelMappings:     `[{"github_label":"broken","windshift_label_id":999999}]`,
	}, &Issue{
		ID:        101,
		Number:    7,
		Title:     "Rollback issue creation",
		State:     "open",
		URL:       "https://example.com/windshift/atomicity/issues/7",
		Labels:    []IssueLabel{{Name: "broken"}},
		Milestone: &IssueMilestone{Number: 7},
	})
	if err == nil || !strings.Contains(err.Error(), "replace mapped labels") {
		t.Fatalf("create error = %v, want mapped label failure", err)
	}

	var itemCount, milestoneCount, trackingCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM items WHERE title = ?", "Rollback issue creation").Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM item_milestones").Scan(&milestoneCount); err != nil {
		t.Fatalf("count item milestones: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM issue_sync_items WHERE issue_sync_config_id = ?", configID).Scan(&trackingCount); err != nil {
		t.Fatalf("count issue sync tracking: %v", err)
	}
	if itemCount != 0 || milestoneCount != 0 || trackingCount != 0 {
		t.Fatalf("rollback counts = items %d milestones %d tracking %d, want all zero", itemCount, milestoneCount, trackingCount)
	}
}

func TestIssueSyncMappedLabelFailureRollsBackItemUpdate(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	itemID, err := factory.NewTestFactory(db).CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	labelRepo := repository.NewLabelRepository(db)
	existingLabelID, _, err := labelRepo.Create("existing", "336699")
	if err != nil {
		t.Fatalf("create existing label: %v", err)
	}
	if err := labelRepo.AddItemLabel(itemID, int(existingLabelID)); err != nil {
		t.Fatalf("attach existing label: %v", err)
	}
	syncItemID := createIssueSyncTracking(t, db, data.WorkspaceID, itemID)

	svc := NewIssueSyncService(db, nil)
	err = svc.updateItemFromIssue(t.Context(), &models.IssueSyncConfig{
		WorkspaceID:   data.WorkspaceID,
		StatusMapping: fmt.Sprintf(`{"open":%d}`, data.StatusID),
		LabelSyncMode: models.IssueSyncLabelMapped,
		LabelMappings: `[{"github_label":"broken","windshift_label_id":999999}]`,
	}, &Issue{
		Title:  "Changed by issue sync",
		State:  "open",
		Labels: []IssueLabel{{Name: "broken"}},
	}, itemID, syncItemID)
	if err == nil || !strings.Contains(err.Error(), "replace mapped labels") {
		t.Fatalf("update error = %v, want mapped label failure", err)
	}

	var title string
	if err := db.QueryRow("SELECT title FROM items WHERE id = ?", itemID).Scan(&title); err != nil {
		t.Fatalf("read item title: %v", err)
	}
	if title == "Changed by issue sync" {
		t.Fatal("item title was committed despite label sync failure")
	}

	labels, err := labelRepo.ListForItem(itemID)
	if err != nil {
		t.Fatalf("list item labels: %v", err)
	}
	if len(labels) != 1 || labels[0].ID != int(existingLabelID) {
		t.Fatalf("labels after failed sync = %#v, want existing label %d", labels, existingLabelID)
	}
}

func TestIssueSyncMissingTrackingRowRollsBackItemUpdate(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	itemID, err := factory.NewTestFactory(db).CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	err = NewIssueSyncService(db, nil).updateItemFromIssue(t.Context(), &models.IssueSyncConfig{
		WorkspaceID:   data.WorkspaceID,
		StatusMapping: fmt.Sprintf(`{"open":%d}`, data.StatusID),
		LabelSyncMode: models.IssueSyncLabelNone,
	}, &Issue{Title: "Changed by issue sync", State: "open"}, itemID, 999999)
	if err == nil || !strings.Contains(err.Error(), "sync item 999999 not found") {
		t.Fatalf("update error = %v, want missing sync item failure", err)
	}

	var title string
	if err := db.QueryRow("SELECT title FROM items WHERE id = ?", itemID).Scan(&title); err != nil {
		t.Fatalf("read item title: %v", err)
	}
	if title == "Changed by issue sync" {
		t.Fatal("item title was committed without updating sync metadata")
	}
}

func TestIssueSyncUpdateUsesCanonicalTitleValidation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	itemID, err := factory.NewTestFactory(db).CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	syncItemID := createIssueSyncTracking(t, db, data.WorkspaceID, itemID)

	err = NewIssueSyncService(db, nil).updateItemFromIssue(t.Context(), &models.IssueSyncConfig{
		WorkspaceID:   data.WorkspaceID,
		StatusMapping: fmt.Sprintf(`{"open":%d}`, data.StatusID),
		LabelSyncMode: models.IssueSyncLabelNone,
	}, &Issue{
		Title: "  Changed by issue sync  ",
		State: "open",
	}, itemID, syncItemID)
	if err != nil {
		t.Fatalf("update item from issue: %v", err)
	}

	var title string
	if err := db.QueryRow("SELECT title FROM items WHERE id = ?", itemID).Scan(&title); err != nil {
		t.Fatalf("read item title: %v", err)
	}
	if title != "Changed by issue sync" {
		t.Fatalf("title = %q, want canonical title %q", title, "Changed by issue sync")
	}
}
