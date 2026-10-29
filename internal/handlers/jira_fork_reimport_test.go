package handlers

import (
	"database/sql"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
)

// TestForcedJiraReimportIntoNewWorkspaceForksSubresources is the WI-1175
// regression: force re-import into a changed project→workspace mapping must
// fork the item *and* its comments and attachments, leaving the old
// workspace's rows untouched instead of moving them to the new item.
func TestForcedJiraReimportIntoNewWorkspaceForksSubresources(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	attachmentRoot := t.TempDir()
	handler := NewJiraImportHandler(db, "jira-fork-test-secret", "", blobstore.NewLocal(attachmentRoot))

	sourceWorkspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	targetWorkspaceID := createJiraForkWorkspace(t, db)

	if _, err := db.ExecWrite(`
		INSERT INTO attachment_settings (attachment_path, enabled)
		VALUES (?, true)
	`, attachmentRoot); err != nil {
		t.Fatalf("configure attachments: %v", err)
	}

	issue := jira.JiraIssue{
		ID:  "10001",
		Key: "APP-1",
		Fields: jira.JiraIssueFields{
			Summary:   "Forked issue",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			Attachment: []jira.JiraAttachment{{
				ID:       "attachment-1",
				Filename: "evidence.txt",
				Content:  "https://example.invalid/attachment-1",
			}},
			Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{{
				ID:   "comment-1",
				Body: "Original comment",
			}}},
		},
	}

	importInto := func(jobID string, workspaceID int, reimport bool) {
		t.Helper()
		im := &jiraImportContext{
			jobID:             jobID,
			forceReimport:     reimport,
			progress:          &ImportProgress{},
			workspaceID:       workspaceID,
			statusMap:         map[string]int{"status": statusID},
			itemTypeMap:       map[string]int{"type": itemTypeID},
			userMap:           map[string]int{},
			usernameMap:       map[string]string{},
			portalCustomerMap: map[string]int{},
			versionMap:        map[string]int{},
			iterationMap:      map[string]int{},
			customFieldIDMap:  map[string]int{},
			choiceOptionIDs:   map[string]map[string]int{},
			client:            &fakeJiraAttachmentImportClient{},
		}
		if err := handler.importIssue(t.Context(), im, &issue); err != nil {
			t.Fatalf("import issue into workspace %d (job %s): %v", workspaceID, jobID, err)
		}
	}

	importInto("job-jsm", sourceWorkspaceID, false)
	insertJiraImportJob(t, db, "job-fork", "conn-jsm", "running", []string{"APP"})
	importInto("job-fork", targetWorkspaceID, true)

	// Each workspace keeps exactly one item with its own comment and attachment.
	sourceItemID := jiraItemIDForWorkspace(t, db, sourceWorkspaceID)
	targetItemID := jiraItemIDForWorkspace(t, db, targetWorkspaceID)
	if sourceItemID == targetItemID {
		t.Fatalf("fork reused item %d instead of creating a new one", sourceItemID)
	}

	sourceCommentID := jiraOnlyCommentID(t, db, sourceItemID)
	targetCommentID := jiraOnlyCommentID(t, db, targetItemID)
	if sourceCommentID == targetCommentID {
		t.Fatalf("fork moved comment %d to the new workspace instead of forking it", sourceCommentID)
	}
	sourceAttachmentID := jiraOnlyAttachmentID(t, db, sourceItemID)
	targetAttachmentID := jiraOnlyAttachmentID(t, db, targetItemID)
	if sourceAttachmentID == targetAttachmentID {
		t.Fatalf("fork moved attachment %d to the new workspace instead of forking it", sourceAttachmentID)
	}

	// The old job still owns the source rows; the new job owns the forked rows.
	if got := jiraMappingWindshiftID(t, db, "job-jsm", "comment", "comment-1"); got != sourceCommentID {
		t.Fatalf("job-jsm comment mapping = %d, want source comment %d", got, sourceCommentID)
	}
	if got := jiraMappingWindshiftID(t, db, "job-fork", "comment", "comment-1"); got != targetCommentID {
		t.Fatalf("job-fork comment mapping = %d, want forked comment %d", got, targetCommentID)
	}
	if got := jiraMappingWindshiftID(t, db, "job-jsm", "attachment", "attachment-1"); got != sourceAttachmentID {
		t.Fatalf("job-jsm attachment mapping = %d, want source attachment %d", got, sourceAttachmentID)
	}
	if got := jiraMappingWindshiftID(t, db, "job-fork", "attachment", "attachment-1"); got != targetAttachmentID {
		t.Fatalf("job-fork attachment mapping = %d, want forked attachment %d", got, targetAttachmentID)
	}
}

func createJiraForkWorkspace(t *testing.T, db database.Database) int {
	t.Helper()
	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active)
		VALUES ('Jira Fork Target', 'FRK', true)
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create fork workspace: %v", err)
	}
	return workspaceID
}

func jiraItemIDForWorkspace(t *testing.T, db database.Database, workspaceID int) int {
	t.Helper()
	var itemID, count int
	if err := db.QueryRow(`SELECT COUNT(*), MIN(id) FROM items WHERE workspace_id = ?`, workspaceID).Scan(&count, &itemID); err != nil {
		t.Fatalf("load item for workspace %d: %v", workspaceID, err)
	}
	if count != 1 {
		t.Fatalf("workspace %d has %d items, want 1", workspaceID, count)
	}
	return itemID
}

func jiraOnlyCommentID(t *testing.T, db database.Database, itemID int) int {
	t.Helper()
	return jiraOnlyOwnedID(t, db, "comments", "comment", itemID)
}

func jiraOnlyAttachmentID(t *testing.T, db database.Database, itemID int) int {
	t.Helper()
	return jiraOnlyOwnedID(t, db, "attachments", "attachment", itemID)
}

func jiraOnlyWorklogID(t *testing.T, db database.Database, itemID int) int {
	t.Helper()
	return jiraOnlyOwnedID(t, db, "time_worklogs", "worklog", itemID)
}

// jiraOnlyOwnedID asserts the item owns exactly one of the given subresource and
// returns its id, reporting a missing row clearly instead of a NULL scan error.
func jiraOnlyOwnedID(t *testing.T, db database.Database, table, label string, itemID int) int {
	t.Helper()
	var count int
	var ownedID sql.NullInt64
	if err := db.QueryRow(`SELECT COUNT(*), MIN(id) FROM `+table+` WHERE item_id = ?`, itemID).Scan(&count, &ownedID); err != nil {
		t.Fatalf("load %s for item %d: %v", label, itemID, err)
	}
	if count != 1 || !ownedID.Valid {
		t.Fatalf("item %d has %d %ss, want 1", itemID, count, label)
	}
	return int(ownedID.Int64)
}

func jiraMappingWindshiftID(t *testing.T, db database.Database, jobID, entityType, jiraID string) int {
	t.Helper()
	var windshiftID int
	if err := db.QueryRow(`
		SELECT windshift_id FROM jira_import_id_mappings
		WHERE job_id = ? AND entity_type = ? AND jira_id = ?
	`, jobID, entityType, jiraID).Scan(&windshiftID); err != nil {
		t.Fatalf("load %s mapping %s/%s: %v", entityType, jobID, jiraID, err)
	}
	return windshiftID
}

// TestJiraPartialForkRetryDoesNotMoveOriginalSubresources is the WI-1716
// regression: when a fork creates the destination item but omits a subresource,
// retrying into the destination must create new destination rows instead of
// reusing (and thereby moving) the original workspace's comment, attachment, or
// worklog.
func TestJiraPartialForkRetryDoesNotMoveOriginalSubresources(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	attachmentRoot := t.TempDir()
	handler := NewJiraImportHandler(db, "jira-partial-fork-secret", "", blobstore.NewLocal(attachmentRoot))

	sourceWorkspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	targetWorkspaceID := createJiraForkWorkspace(t, db)

	if _, err := db.ExecWrite(`
		INSERT INTO attachment_settings (attachment_path, enabled)
		VALUES (?, true)
	`, attachmentRoot); err != nil {
		t.Fatalf("configure attachments: %v", err)
	}

	var customerOrgID, timeProjectID int
	if err := db.QueryRow(`INSERT INTO customer_organisations (name) VALUES ('Fork Customer') RETURNING id`).Scan(&customerOrgID); err != nil {
		t.Fatalf("create customer organisation: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO time_projects (name, status, active, customer_id)
		VALUES ('Fork project', 'Active', true, ?)
		RETURNING id
	`, customerOrgID).Scan(&timeProjectID); err != nil {
		t.Fatalf("create time project: %v", err)
	}

	insertJiraImportConnection(t, db, "conn-partial")
	insertJiraImportJob(t, db, "job-source", "conn-partial", "running", []string{"APP"})
	insertJiraImportJob(t, db, "job-target-first", "conn-partial", "running", []string{"APP"})
	insertJiraImportJob(t, db, "job-target-retry", "conn-partial", "running", []string{"APP"})

	fullIssue := func() *jira.JiraIssue {
		return &jira.JiraIssue{
			ID:  "10001",
			Key: "APP-1",
			Fields: jira.JiraIssueFields{
				Summary:   "Partial fork issue",
				IssueType: &jira.JiraIssueType{ID: "type"},
				Status:    &jira.JiraStatus{ID: "status"},
				Attachment: []jira.JiraAttachment{{
					ID: "attachment-1", Filename: "evidence.txt", Content: "https://example.invalid/attachment-1",
				}},
				Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{{ID: "comment-1", Body: "Original comment"}}},
				Worklog: &jira.JiraWorklogContainer{Worklogs: []jira.JiraWorklog{{
					ID: "worklog-1", TimeSpentSeconds: 3600, Started: "2026-01-02T09:00:00.000+0000", TimeSpent: "1h",
				}}},
			},
		}
	}
	itemOnlyIssue := func() *jira.JiraIssue {
		issue := fullIssue()
		issue.Fields.Attachment = nil
		issue.Fields.Comment = nil
		issue.Fields.Worklog = nil
		return issue
	}

	importInto := func(jobID string, workspaceID int, issue *jira.JiraIssue, reimport bool, timeProject *int) {
		t.Helper()
		im := &jiraImportContext{
			jobID:             jobID,
			forceReimport:     reimport,
			progress:          &ImportProgress{},
			workspaceID:       workspaceID,
			timeProjectID:     timeProject,
			statusMap:         map[string]int{"status": statusID},
			itemTypeMap:       map[string]int{"type": itemTypeID},
			userMap:           map[string]int{},
			usernameMap:       map[string]string{},
			portalCustomerMap: map[string]int{},
			versionMap:        map[string]int{},
			iterationMap:      map[string]int{},
			customFieldIDMap:  map[string]int{},
			choiceOptionIDs:   map[string]map[string]int{},
			client:            &fakeJiraAttachmentImportClient{},
		}
		if err := handler.importIssue(t.Context(), im, issue); err != nil {
			t.Fatalf("import issue into workspace %d (job %s): %v", workspaceID, jobID, err)
		}
	}

	// Import everything into the source, fork the item into the target without
	// its subresources, then retry the target with the subresources available.
	importInto("job-source", sourceWorkspaceID, fullIssue(), false, &timeProjectID)
	importInto("job-target-first", targetWorkspaceID, itemOnlyIssue(), true, nil)
	importInto("job-target-retry", targetWorkspaceID, fullIssue(), true, &timeProjectID)

	sourceItemID := jiraItemIDForWorkspace(t, db, sourceWorkspaceID)
	targetItemID := jiraItemIDForWorkspace(t, db, targetWorkspaceID)
	if sourceItemID == targetItemID {
		t.Fatalf("target import reused source item %d", sourceItemID)
	}

	sourceCommentID := jiraOnlyCommentID(t, db, sourceItemID)
	targetCommentID := jiraOnlyCommentID(t, db, targetItemID)
	if sourceCommentID == targetCommentID {
		t.Fatalf("partial-fork retry moved comment %d out of the source item", sourceCommentID)
	}
	sourceAttachmentID := jiraOnlyAttachmentID(t, db, sourceItemID)
	targetAttachmentID := jiraOnlyAttachmentID(t, db, targetItemID)
	if sourceAttachmentID == targetAttachmentID {
		t.Fatalf("partial-fork retry moved attachment %d out of the source item", sourceAttachmentID)
	}
	sourceWorklogID := jiraOnlyWorklogID(t, db, sourceItemID)
	targetWorklogID := jiraOnlyWorklogID(t, db, targetItemID)
	if sourceWorklogID == targetWorklogID {
		t.Fatalf("partial-fork retry moved worklog %d out of the source item", sourceWorklogID)
	}

	// The source rows keep their original ownership and content.
	var sourceCommentContent, sourceAttachmentFilename, sourceWorklogDescription string
	if err := db.QueryRow(`SELECT content FROM comments WHERE id = ?`, sourceCommentID).Scan(&sourceCommentContent); err != nil {
		t.Fatalf("load source comment: %v", err)
	}
	if sourceCommentContent != "Original comment" {
		t.Fatalf("source comment content = %q, want original", sourceCommentContent)
	}
	if err := db.QueryRow(`SELECT original_filename FROM attachments WHERE id = ?`, sourceAttachmentID).Scan(&sourceAttachmentFilename); err != nil {
		t.Fatalf("load source attachment: %v", err)
	}
	if sourceAttachmentFilename != "evidence.txt" {
		t.Fatalf("source attachment filename = %q, want evidence.txt", sourceAttachmentFilename)
	}
	if err := db.QueryRow(`SELECT description FROM time_worklogs WHERE id = ?`, sourceWorklogID).Scan(&sourceWorklogDescription); err != nil {
		t.Fatalf("load source worklog: %v", err)
	}
	if sourceWorklogDescription != "1h" {
		t.Fatalf("source worklog description = %q, want 1h", sourceWorklogDescription)
	}

	// The source job still owns the source rows; the retry owns the target rows.
	if got := jiraMappingWindshiftID(t, db, "job-source", "comment", "comment-1"); got != sourceCommentID {
		t.Fatalf("job-source comment mapping = %d, want source comment %d", got, sourceCommentID)
	}
	if got := jiraMappingWindshiftID(t, db, "job-target-retry", "comment", "comment-1"); got != targetCommentID {
		t.Fatalf("job-target-retry comment mapping = %d, want target comment %d", got, targetCommentID)
	}
	if got := jiraMappingWindshiftID(t, db, "job-source", "attachment", "attachment-1"); got != sourceAttachmentID {
		t.Fatalf("job-source attachment mapping = %d, want source attachment %d", got, sourceAttachmentID)
	}
	if got := jiraMappingWindshiftID(t, db, "job-target-retry", "attachment", "attachment-1"); got != targetAttachmentID {
		t.Fatalf("job-target-retry attachment mapping = %d, want target attachment %d", got, targetAttachmentID)
	}
	if got := jiraMappingWindshiftID(t, db, "job-source", "worklog", "worklog-1"); got != sourceWorklogID {
		t.Fatalf("job-source worklog mapping = %d, want source worklog %d", got, sourceWorklogID)
	}
	if got := jiraMappingWindshiftID(t, db, "job-target-retry", "worklog", "worklog-1"); got != targetWorklogID {
		t.Fatalf("job-target-retry worklog mapping = %d, want target worklog %d", got, targetWorklogID)
	}
}
