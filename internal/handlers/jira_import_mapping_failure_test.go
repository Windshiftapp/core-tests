package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
	"windshift/internal/testutils"
)

func installJiraMappingInsertFailure(t *testing.T, db *testutils.TestDB) {
	t.Helper()
	if db.Engine == "postgres" {
		if _, err := db.ExecWrite(`
			CREATE FUNCTION fail_jira_mapping_insert() RETURNS trigger AS $func$
			BEGIN
				RAISE EXCEPTION 'injected Jira mapping failure';
				RETURN NEW;
			END
			$func$ LANGUAGE plpgsql
		`); err != nil {
			t.Fatalf("create Jira mapping failure function: %v", err)
		}
		if _, err := db.ExecWrite(`
			CREATE TRIGGER fail_jira_mapping_insert
			BEFORE INSERT OR UPDATE ON jira_import_id_mappings
			FOR EACH ROW EXECUTE FUNCTION fail_jira_mapping_insert()
		`); err != nil {
			t.Fatalf("create Jira mapping failure trigger: %v", err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecWrite(`DROP TRIGGER IF EXISTS fail_jira_mapping_insert ON jira_import_id_mappings`)
			_, _ = db.ExecWrite(`DROP FUNCTION IF EXISTS fail_jira_mapping_insert()`)
		})
		return
	}

	if _, err := db.ExecWrite(`
		CREATE TRIGGER fail_jira_mapping_insert
		BEFORE INSERT ON jira_import_id_mappings
		BEGIN
			SELECT RAISE(ABORT, 'injected Jira mapping failure');
		END
	`); err != nil {
		t.Fatalf("create Jira mapping failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecWrite(`DROP TRIGGER IF EXISTS fail_jira_mapping_insert`)
	})
}

func TestRecordJiraImportMappingFailureIsReturnedForMappedEntities(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	insertJiraImportConnection(t, db.Database, "conn-mapping-failure")
	insertJiraImportJob(t, db.Database, "job-mapping-failure", "conn-mapping-failure", "running", []string{"APP"})

	handler := NewJiraImportHandler(db.Database, "jira-mapping-failure-test-secret", "", blobstore.Disabled())
	claimJiraImportJobForTest(t, handler, db.Database, "job-mapping-failure")
	installJiraMappingInsertFailure(t, db)

	tests := []struct {
		entityType string
		jiraID     string
	}{
		{entityType: "item", jiraID: "APP-1"},
		{entityType: "comment", jiraID: "comment-1"},
		{entityType: "watch", jiraID: "APP-1:user-1"},
		{entityType: "link", jiraID: "link-1"},
	}
	for index, tt := range tests {
		t.Run(tt.entityType, func(t *testing.T) {
			err := handler.recordMapping(
				"job-mapping-failure",
				tt.entityType,
				tt.jiraID,
				"APP-1",
				index+1,
				map[string]interface{}{"action": "create"},
			)
			if err == nil || !strings.Contains(err.Error(), "persist mapping provenance") {
				t.Fatalf("recordMapping() error = %v, want persisted-mapping failure", err)
			}
		})
	}

	progress := &ImportProgress{Phase: "items"}
	if !handler.failOnMappingFailure("job-mapping-failure", progress) {
		t.Fatal("failOnMappingFailure() = false, want true")
	}
	if progress.Phase != "failed" {
		t.Fatalf("progress phase = %q, want failed", progress.Phase)
	}

	var status, phase, errorMessage string
	if err := db.QueryRow(`
		SELECT status, phase, error_message
		FROM jira_import_jobs
		WHERE id = ?
	`, "job-mapping-failure").Scan(&status, &phase, &errorMessage); err != nil {
		t.Fatalf("load failed import job: %v", err)
	}
	if status != "failed" || phase != "mapping" {
		t.Fatalf("job status/phase = %q/%q, want failed/mapping", status, phase)
	}
	if !strings.Contains(errorMessage, "Failed to persist Jira import mapping") ||
		!strings.Contains(errorMessage, "injected Jira mapping failure") {
		t.Fatalf("job error message = %q, want mapping failure details", errorMessage)
	}

	var mappingCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM jira_import_id_mappings WHERE job_id = ?
	`, "job-mapping-failure").Scan(&mappingCount); err != nil {
		t.Fatalf("count failed mappings: %v", err)
	}
	if mappingCount != 0 {
		t.Fatalf("failed mapping rows = %d, want 0", mappingCount)
	}
}

func TestJiraReimportMappingFailurePreservesPreviousOwnership(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	insertJiraImportConnection(t, db.Database, "conn-reimport-mapping-failure")
	insertJiraImportJob(t, db.Database, "job-original", "conn-reimport-mapping-failure", "completed", []string{"APP"})

	handler := NewJiraImportHandler(db.Database, "jira-reimport-mapping-failure-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db.Database)
	issue := jira.JiraIssue{
		ID:  "10001",
		Key: "APP-1",
		Fields: jira.JiraIssueFields{
			Summary:   "Original summary",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
		},
	}
	importJiraReimportIssue(t, handler, "job-original", workspaceID, itemTypeID, statusID, &issue, false)

	insertJiraImportJob(t, db.Database, "job-reimport", "conn-reimport-mapping-failure", "running", []string{"APP"})
	claimJiraImportJobForTest(t, handler, db.Database, "job-reimport")
	installJiraMappingInsertFailure(t, db)
	issue.Fields.Summary = "Updated summary"
	progress := &ImportProgress{}
	im := &jiraImportContext{
		jobID:             "job-reimport",
		forceReimport:     true,
		progress:          progress,
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
	}
	err := handler.importIssue(context.Background(), im, &issue)
	if err == nil || !strings.Contains(err.Error(), "record Jira item mapping") {
		t.Fatalf("reimport error = %v, want item mapping failure", err)
	}

	var oldMetadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json
		FROM jira_import_id_mappings
		WHERE job_id = ? AND entity_type = 'item' AND jira_id = ?
	`, "job-original", issue.ID).Scan(&oldMetadataJSON); err != nil {
		t.Fatalf("load previous mapping: %v", err)
	}
	var oldMetadata map[string]interface{}
	if err := json.Unmarshal([]byte(oldMetadataJSON), &oldMetadata); err != nil {
		t.Fatalf("decode previous mapping metadata: %v", err)
	}
	if oldMetadata["was_created"] != true {
		t.Fatalf("previous mapping was_created = %#v, want true", oldMetadata["was_created"])
	}
	if _, superseded := oldMetadata["superseded_by_job_id"]; superseded {
		t.Fatalf("previous mapping was superseded after failed reimport: %#v", oldMetadata)
	}

	var newMappingCount int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM jira_import_id_mappings
		WHERE job_id = ? AND entity_type = 'item' AND jira_id = ?
	`, "job-reimport", issue.ID).Scan(&newMappingCount); err != nil {
		t.Fatalf("count replacement mapping: %v", err)
	}
	if newMappingCount != 0 {
		t.Fatalf("replacement mapping count = %d, want 0", newMappingCount)
	}

	if !handler.failOnMappingFailure("job-reimport", progress) {
		t.Fatal("failOnMappingFailure() = false, want true")
	}
	var status, phase string
	if err := db.QueryRow(`
		SELECT status, phase FROM jira_import_jobs WHERE id = ?
	`, "job-reimport").Scan(&status, &phase); err != nil {
		t.Fatalf("load reimport job status: %v", err)
	}
	if status != "failed" || phase != "mapping" {
		t.Fatalf("reimport job status/phase = %q/%q, want failed/mapping", status, phase)
	}
}
