//go:build test

package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
	"windshift/internal/testutils"
	"windshift/internal/xray"
)

func TestJiraImportJobConfigNeverPersistsXrayCloudSecret(t *testing.T) {
	config, err := jiraImportJobConfigJSON(StartImportRequest{
		ProjectKeys: []string{"QA"},
		Xray: XrayImportOptions{
			ImportTests:  true,
			Region:       "eu",
			ClientID:     "client-id",
			ClientSecret: "do-not-persist-this",
		},
	})
	if err != nil {
		t.Fatalf("jiraImportJobConfigJSON() error = %v", err)
	}
	if strings.Contains(string(config), "do-not-persist-this") ||
		strings.Contains(string(config), "client-id") {
		t.Fatalf("persisted config contains Xray credentials: %s", config)
	}
	if !strings.Contains(string(config), `"import_tests":true`) {
		t.Fatalf("persisted config lost non-secret Xray choice: %s", config)
	}
}

func TestImportXrayTestCaseCreatesOrderedStepsAndMapping(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Quality', 'QA') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO jira_import_connections
			(id, instance_url, email, encrypted_credentials, instance_name, deployment_type)
		VALUES ('connection-1', 'https://example.atlassian.net', 'admin@example.com', 'secret', 'Example', 'cloud')
	`); err != nil {
		t.Fatalf("insert Jira connection: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO jira_import_jobs (id, connection_id, status, scope, config_json)
		VALUES ('job-1', 'connection-1', 'running', 'work_items', '{}')
	`); err != nil {
		t.Fatalf("insert Jira import job: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-xray-import-test-secret-32-bytes", "", blobstore.Disabled())
	issue := jira.JiraIssue{
		ID:  "10001",
		Key: "QA-7",
		Fields: jira.JiraIssueFields{
			Summary:  "A customer can sign in",
			Priority: &jira.JiraPriority{Name: "Highest"},
			Labels:   []string{"smoke", "customer"},
		},
	}
	definition := xray.Test{
		IssueID:      "10001",
		TestTypeName: "Manual",
		Steps: []xray.Step{
			{Action: "Open sign in", Data: "alice@example.com", Expected: "Form is shown"},
			{Action: "Submit", Expected: "Dashboard opens"},
		},
	}

	testCaseID, err := handler.importXrayTestCase("job-1", workspaceID, &issue, definition)
	if err != nil {
		t.Fatalf("importXrayTestCase() error = %v", err)
	}

	var title, priority string
	if err := tdb.QueryRow(`
		SELECT title, priority FROM test_cases WHERE id = ?
	`, testCaseID).Scan(&title, &priority); err != nil {
		t.Fatalf("load test case: %v", err)
	}
	if title != "A customer can sign in" || priority != "critical" {
		t.Fatalf("test case title=%q priority=%q", title, priority)
	}

	rows, err := tdb.Query(`
		SELECT step_number, action, data, expected
		FROM test_steps
		WHERE test_case_id = ?
		ORDER BY step_number
	`, testCaseID)
	if err != nil {
		t.Fatalf("load steps: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []xray.Step
	var stepNumbers []int
	for rows.Next() {
		var step xray.Step
		var number int
		if err := rows.Scan(&number, &step.Action, &step.Data, &step.Expected); err != nil {
			t.Fatalf("scan step: %v", err)
		}
		stepNumbers = append(stepNumbers, number)
		got = append(got, step)
	}
	if len(got) != 2 || stepNumbers[0] != 1 || stepNumbers[1] != 2 {
		t.Fatalf("steps = %#v numbers=%#v", got, stepNumbers)
	}
	if got[0] != definition.Steps[0] || got[1] != definition.Steps[1] {
		t.Fatalf("steps = %#v, want %#v", got, definition.Steps)
	}

	var mappedID int
	if err := tdb.QueryRow(`
		SELECT windshift_id
		FROM jira_import_id_mappings
		WHERE job_id = 'job-1' AND entity_type = 'test_case' AND jira_id = '10001'
	`).Scan(&mappedID); err != nil {
		t.Fatalf("load mapping: %v", err)
	}
	if mappedID != testCaseID {
		t.Fatalf("mapped ID = %d, want %d", mappedID, testCaseID)
	}
}

func TestImportXrayTestCaseRollsBackWhenStepCreationFails(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	workspaceID := createXrayImportTestJob(t, tdb, "job-rollback", "running")
	if tdb.Engine == "postgres" {
		if _, err := tdb.ExecWrite(`
			CREATE FUNCTION fail_second_xray_step() RETURNS trigger AS $$
			BEGIN
				IF NEW.step_number = 2 THEN
					RAISE EXCEPTION 'forced step failure';
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql
		`); err != nil {
			t.Fatalf("create failure function: %v", err)
		}
		if _, err := tdb.ExecWrite(`
			CREATE TRIGGER fail_second_xray_step
			BEFORE INSERT ON test_steps
			FOR EACH ROW EXECUTE FUNCTION fail_second_xray_step()
		`); err != nil {
			t.Fatalf("create failure trigger: %v", err)
		}
	} else {
		if _, err := tdb.ExecWrite(`
			CREATE TRIGGER fail_second_xray_step
			BEFORE INSERT ON test_steps
			WHEN NEW.step_number = 2
			BEGIN
				SELECT RAISE(ABORT, 'forced step failure');
			END
		`); err != nil {
			t.Fatalf("create failure trigger: %v", err)
		}
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-xray-import-test-secret-32-bytes", "", blobstore.Disabled())
	_, err := handler.importXrayTestCase(
		"job-rollback",
		workspaceID,
		&jira.JiraIssue{
			ID:  "10002",
			Key: "QA-8",
			Fields: jira.JiraIssueFields{
				Summary: "A failed atomic import",
			},
		},
		xray.Test{
			IssueID:      "10002",
			TestTypeName: "Manual",
			Steps: []xray.Step{
				{Action: "First"},
				{Action: "Second"},
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "create Xray Test step 2") {
		t.Fatalf("importXrayTestCase() error = %v, want second-step failure", err)
	}

	for _, check := range []struct {
		name  string
		query string
		arg   any
	}{
		{name: "test cases", query: `SELECT COUNT(*) FROM test_cases WHERE workspace_id = ?`, arg: workspaceID},
		{name: "mappings", query: `SELECT COUNT(*) FROM jira_import_id_mappings WHERE job_id = ?`, arg: "job-rollback"},
	} {
		var count int
		if err := tdb.QueryRow(check.query, check.arg).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s remaining after rollback = %d, want 0", check.name, count)
		}
	}
}

func TestDeleteImportedDataRemovesXrayTestCaseAndSteps(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	workspaceID := createXrayImportTestJob(t, tdb, "job-cleanup", "completed")
	handler := NewJiraImportHandler(tdb.Database, "jira-xray-import-test-secret-32-bytes", "", blobstore.Disabled())
	testCaseID, err := handler.importXrayTestCase(
		"job-cleanup",
		workspaceID,
		&jira.JiraIssue{
			ID:  "10003",
			Key: "QA-9",
			Fields: jira.JiraIssueFields{
				Summary: "A test that is later removed",
			},
		},
		xray.Test{
			IssueID:      "10003",
			TestTypeName: "Manual",
			Steps:        []xray.Step{{Action: "Perform action", Expected: "Observe result"}},
		},
	)
	if err != nil {
		t.Fatalf("importXrayTestCase() error = %v", err)
	}

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/admin/jira-import/jobs/job-cleanup/data",
		bytes.NewBufferString(`{"confirm_job_id":"job-cleanup","confirm_workspace_count":0,"confirm_delete_imported_data":true}`),
	)
	req.SetPathValue("jobId", "job-cleanup")
	w := httptest.NewRecorder()
	handler.DeleteImportedData(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteImportedData() status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	for _, check := range []struct {
		name  string
		query string
		arg   any
	}{
		{name: "test cases", query: `SELECT COUNT(*) FROM test_cases WHERE id = ?`, arg: testCaseID},
		{name: "test steps", query: `SELECT COUNT(*) FROM test_steps WHERE test_case_id = ?`, arg: testCaseID},
		{name: "mappings", query: `SELECT COUNT(*) FROM jira_import_id_mappings WHERE job_id = ?`, arg: "job-cleanup"},
	} {
		var count int
		if err := tdb.QueryRow(check.query, check.arg).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s remaining after cleanup = %d, want 0", check.name, count)
		}
	}
}

func createXrayImportTestJob(
	t *testing.T,
	tdb *testutils.TestDB,
	jobID, status string,
) int {
	t.Helper()
	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Quality', 'QA') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO jira_import_connections
			(id, instance_url, email, encrypted_credentials, instance_name, deployment_type)
		VALUES ('connection-xray', 'https://example.atlassian.net', 'admin@example.com', 'secret', 'Example', 'cloud')
	`); err != nil {
		t.Fatalf("insert Jira connection: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO jira_import_jobs (id, connection_id, status, scope, config_json)
		VALUES (?, 'connection-xray', ?, 'work_items', '{}')
	`, jobID, status); err != nil {
		t.Fatalf("insert Jira import job: %v", err)
	}
	return workspaceID
}
