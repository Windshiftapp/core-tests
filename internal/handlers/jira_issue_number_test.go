package handlers

import (
	"encoding/json"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
	"windshift/internal/services"
)

func jiraIssueWithNumber(id, key, summary string) jira.JiraIssue {
	return jira.JiraIssue{
		ID:  id,
		Key: key,
		Fields: jira.JiraIssueFields{
			Summary:   summary,
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
		},
	}
}

func itemNumberByTitle(t *testing.T, db database.Database, title string) int {
	t.Helper()
	var number int
	if err := db.QueryRow(`SELECT workspace_item_number FROM items WHERE title = ?`, title).Scan(&number); err != nil {
		t.Fatalf("load item number for %q: %v", title, err)
	}
	return number
}

func TestJiraIssueNumberParsesNumericSuffix(t *testing.T) {
	tests := []struct {
		key  string
		want int
		ok   bool
	}{
		{key: "EXPL-30", want: 30, ok: true},
		{key: "PROJECT-WITH-DASHES-42", want: 42, ok: true},
		{key: "EXPL-0", ok: false},
		{key: "EXPL-", ok: false},
		{key: "EXPL", ok: false},
		{key: "EXPL-abc", ok: false},
	}
	for _, test := range tests {
		got, ok := jiraIssueNumber(test.key)
		if got != test.want || ok != test.ok {
			t.Fatalf("jiraIssueNumber(%q) = (%d, %t), want (%d, %t)", test.key, got, ok, test.want, test.ok)
		}
	}
}

func TestJiraImportPreservesIssueNumberAsWorkspaceItemNumber(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-issue-number-preserve-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	// Imported out of numeric order: the pinned numbers come from the Jira keys,
	// not the insertion position.
	for _, issue := range []jira.JiraIssue{
		jiraIssueWithNumber("10030", "APP-30", "Thirty"),
		jiraIssueWithNumber("10005", "APP-5", "Five"),
		jiraIssueWithNumber("10002", "APP-2", "Two"),
	} {
		issue := issue
		importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)
	}

	for _, want := range []struct {
		title  string
		number int
	}{{"Two", 2}, {"Five", 5}, {"Thirty", 30}} {
		if got := itemNumberByTitle(t, db, want.title); got != want.number {
			t.Fatalf("item %q number = %d, want %d", want.title, got, want.number)
		}
	}
}

func TestJiraImportFallsBackWhenPinnedIssueNumberTaken(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-issue-number-taken-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	pinned := 5
	if _, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID:      workspaceID,
		Title:            "Existing five",
		PinnedItemNumber: &pinned,
	}); err != nil {
		t.Fatalf("seed item with pinned number 5: %v", err)
	}

	issue := jiraIssueWithNumber("10005", "APP-5", "Imported five")
	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)

	if got := itemNumberByTitle(t, db, "Existing five"); got != 5 {
		t.Fatalf("existing item number = %d, want unchanged 5", got)
	}
	if got := itemNumberByTitle(t, db, "Imported five"); got != 6 {
		t.Fatalf("imported item number = %d, want fallback 6", got)
	}

	var metadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'fidelity_finding' AND jira_id = 'item-number:APP-5'
	`).Scan(&metadataJSON); err != nil {
		t.Fatalf("load issue-number fallback finding: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode fallback finding metadata: %v", err)
	}
	if metadata["code"] != "jira_issue_number_taken" {
		t.Fatalf("finding code = %v, want jira_issue_number_taken", metadata["code"])
	}
	if metadata["requested_number"] != float64(5) || metadata["assigned_number"] != float64(6) {
		t.Fatalf("finding numbers = %v/%v, want 5/6", metadata["requested_number"], metadata["assigned_number"])
	}
}

func TestJiraImportFallsBackWhenPinnedIssueNumberReserved(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-issue-number-reserved-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	// A delete retires the number, so APP-9 is not free even though no item holds it.
	retired := 9
	retiredID, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID:      workspaceID,
		Title:            "Retired nine",
		PinnedItemNumber: &retired,
	})
	if err != nil {
		t.Fatalf("seed item with pinned number 9: %v", err)
	}
	if err := services.NewItemCRUDService(db).DeleteSingle(int(retiredID)); err != nil {
		t.Fatalf("retire seeded item: %v", err)
	}

	issue := jiraIssueWithNumber("10009", "APP-9", "Imported nine")
	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)

	if got := itemNumberByTitle(t, db, "Imported nine"); got != 10 {
		t.Fatalf("imported item number = %d, want fallback 10", got)
	}
}

func TestJiraReimportKeepsPinnedIssueNumber(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-issue-number-reimport-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	issue := jiraIssueWithNumber("10007", "APP-7", "Seven")
	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)
	if got := itemNumberByTitle(t, db, "Seven"); got != 7 {
		t.Fatalf("initial item number = %d, want 7", got)
	}

	// Push the workspace max above the pinned number so a re-import that created
	// a fresh item would be numbered past 100.
	high := 100
	if _, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID:      workspaceID,
		Title:            "High number",
		PinnedItemNumber: &high,
	}); err != nil {
		t.Fatalf("seed high-numbered item: %v", err)
	}

	insertJiraImportJob(t, db, "job-reimport", "conn-jsm", "running", []string{"APP"})
	issue.Fields.Summary = "Seven updated"
	importJiraReimportIssue(t, handler, "job-reimport", workspaceID, itemTypeID, statusID, &issue, true)

	if got := itemNumberByTitle(t, db, "Seven updated"); got != 7 {
		t.Fatalf("reimported item number = %d, want stable 7", got)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE workspace_id = ? AND title = 'Seven updated'`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count reimported items: %v", err)
	}
	if count != 1 {
		t.Fatalf("reimported item rows = %d, want 1", count)
	}
}
