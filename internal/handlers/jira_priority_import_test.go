package handlers

import (
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
)

// TestJiraImportCreatesCustomPriorityOnDemand verifies a Jira priority without
// a canonical Windshift synonym is preserved and created in the catalog instead
// of collapsing to the default. Reimporting the same name must reuse the row.
func TestJiraImportCreatesCustomPriorityOnDemand(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-priority-on-demand-secret-32", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	issue := jira.JiraIssue{
		ID:  "30001",
		Key: "APP-1",
		Fields: jira.JiraIssueFields{
			Summary:   "Custom priority",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			Priority:  &jira.JiraPriority{ID: "9", Name: "P0"},
		},
	}
	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)

	second := issue
	second.ID = "30002"
	second.Key = "APP-2"
	second.Fields.Summary = "Custom priority again"
	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &second, false)

	priorityName := importedItemPriorityName(t, db, "Custom priority")
	if priorityName != "P0" {
		t.Fatalf("imported priority = %q, want P0", priorityName)
	}
	if again := importedItemPriorityName(t, db, "Custom priority again"); again != "P0" {
		t.Fatalf("second imported priority = %q, want P0", again)
	}

	var created int
	if err := db.QueryRow(`SELECT COUNT(*) FROM priorities WHERE LOWER(name) = 'p0'`).Scan(&created); err != nil {
		t.Fatalf("count created priorities: %v", err)
	}
	if created != 1 {
		t.Fatalf("priorities named P0 = %d, want 1 (created once, reused)", created)
	}
}

// TestJiraImportMapsKnownPriorityToCanonical verifies the default synonym table
// still collapses Jira's built-in priority names onto the canonical catalog.
func TestJiraImportMapsKnownPriorityToCanonical(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-priority-canonical-secret-32", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	issue := jira.JiraIssue{
		ID:  "31001",
		Key: "APP-3",
		Fields: jira.JiraIssueFields{
			Summary:   "Highest priority",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			Priority:  &jira.JiraPriority{ID: "1", Name: "Highest"},
		},
	}
	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)

	if got := importedItemPriorityName(t, db, "Highest priority"); got != "Critical" {
		t.Fatalf("imported priority = %q, want Critical", got)
	}
}

func importedItemPriorityName(t *testing.T, db database.Database, title string) string {
	t.Helper()
	var name string
	if err := db.QueryRow(`
		SELECT p.name FROM items i JOIN priorities p ON p.id = i.priority_id WHERE i.title = ?
	`, title).Scan(&name); err != nil {
		t.Fatalf("load imported item priority for %q: %v", title, err)
	}
	return name
}
