package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/jira"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestGetUserIDFromContextUsesAuthenticatedUser(t *testing.T) {
	want := 42
	req := httptest.NewRequest(http.MethodPost, "/api/admin/jira-import/jobs", nil)
	req = req.WithContext(context.WithValue(req.Context(), contextkeys.User, &models.User{ID: want}))

	got := getUserIDFromContext(req)
	if got == nil || *got != want {
		t.Fatalf("getUserIDFromContext() = %v, want %d", got, want)
	}
}

func TestPersistJiraImportResultIncludesConfigurationFidelity(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	insertJiraImportConnection(t, tdb.Database, "connection-result")
	insertJiraImportJob(t, tdb.Database, "job-result", "connection-result", "running", []string{"APP"})
	planRequest := StartImportRequest{
		ProjectKeys: []string{"APP"},
		Mappings: ImportMappings{
			CustomFields: []CustomFieldMapping{{
				JiraID:        "customfield_20001",
				WindshiftType: string(jira.FieldTypeText),
				Action:        "create",
			}},
		},
	}
	planFingerprint, err := jiraImportPlanFingerprint(planRequest)
	if err != nil {
		t.Fatalf("fingerprint import plan: %v", err)
	}
	configJSON, err := jiraImportJobConfigJSON(planRequest, jiraImportConflict{
		JobID:                   "previous-result-job",
		PreviousPlanFingerprint: "sha256:previous",
		ConfigurationDrift:      true,
	})
	if err != nil {
		t.Fatalf("encode import plan: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		UPDATE jira_import_jobs SET config_json = ? WHERE id = 'job-result'
	`, string(configJSON)); err != nil {
		t.Fatalf("persist import plan: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-import-result-test-secret-32", "", blobstore.Disabled())
	claimJiraImportJobForTest(t, handler, tdb.Database, "job-result")
	handler.recordMapping("job-result", "workflow", "wf-1", "APP - Delivery", 17, map[string]any{
		"fidelity":                       "authoritative_graph",
		"transition_rules_complete":      false,
		"review_locked_transition_count": 3,
	})
	handler.recordMapping("job-result", "screen", "100", "APP - Work", 23, map[string]any{
		"action":         "create",
		"tabs_flattened": true,
	})
	handler.recordMapping("job-result", "custom_field", "customfield_20001", "Environment", 31, map[string]any{
		"action":          "create",
		"option_count":    3,
		"preserve_raw":    true,
		"jira_field_type": "com.atlassian.servicedesk:sd-sla-field",
	})
	handler.persistJiraImportResult("job-result", &ImportProgress{
		Phase:             "completed",
		TotalProjects:     1,
		CompletedProjects: 1,
	})

	var resultJSON string
	if err := tdb.QueryRow(`
		SELECT result_json FROM jira_import_jobs WHERE id = 'job-result'
	`).Scan(&resultJSON); err != nil {
		t.Fatalf("load result_json: %v", err)
	}
	var result struct {
		Summary struct {
			CompletedProjects int `json:"completed_projects"`
		} `json:"summary"`
		Workflows []struct {
			SourceID string                 `json:"source_id"`
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"workflows"`
		Screens []struct {
			SourceID string                 `json:"source_id"`
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"screens"`
		CustomFields []struct {
			SourceID string                 `json:"source_id"`
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"custom_fields"`
		Findings           []jiraImportFidelityFinding `json:"fidelity_findings"`
		PlanFingerprint    string                      `json:"plan_fingerprint"`
		ConfigurationDrift bool                        `json:"configuration_drift"`
		PreviousImports    []jiraImportConflict        `json:"previous_imports"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	if result.Summary.CompletedProjects != 1 {
		t.Fatalf("completed projects = %d, want 1", result.Summary.CompletedProjects)
	}
	if result.PlanFingerprint != planFingerprint || !result.ConfigurationDrift ||
		len(result.PreviousImports) != 1 {
		t.Fatalf("plan result = fingerprint %q drift %t previous %#v", result.PlanFingerprint, result.ConfigurationDrift, result.PreviousImports)
	}
	if len(result.Workflows) != 1 || result.Workflows[0].SourceID != "wf-1" ||
		result.Workflows[0].Metadata["fidelity"] != "authoritative_graph" {
		t.Fatalf("workflow result = %#v", result.Workflows)
	}
	if len(result.Screens) != 1 || result.Screens[0].SourceID != "100" ||
		result.Screens[0].Metadata["tabs_flattened"] != true {
		t.Fatalf("screen result = %#v", result.Screens)
	}
	if len(result.CustomFields) != 1 || result.CustomFields[0].SourceID != "customfield_20001" ||
		result.CustomFields[0].Metadata["option_count"] != float64(3) {
		t.Fatalf("custom field result = %#v", result.CustomFields)
	}
	findingCodes := make(map[string]bool)
	for _, finding := range result.Findings {
		findingCodes[finding.Code] = true
	}
	for _, code := range []string{
		"jira_project_roles_not_imported",
		"jira_permission_schemes_not_imported",
		"jira_custom_fields_preserved_raw",
		"jira_service_sla_state_preserved",
		"jira_import_plan_changed",
	} {
		if !findingCodes[code] {
			t.Fatalf("fidelity findings = %#v, want %q", result.Findings, code)
		}
	}
}

func TestJiraImportPlanFingerprintIsStableAndDetectsMappingChanges(t *testing.T) {
	t.Parallel()

	left := StartImportRequest{
		ProjectKeys:    []string{" SP ", "KAN"},
		OpenIssuesOnly: true,
		ConnectionID:   "connection-a",
		Mappings: ImportMappings{
			Workspaces: []WorkspaceMapping{
				{JiraKey: "SP", IssueCount: 4, CreateNew: true, NewWorkspaceKey: "SP"},
				{JiraKey: "KAN", IssueCount: 100, CreateNew: true, NewWorkspaceKey: "KAN"},
			},
			CustomFields: []CustomFieldMapping{
				{JiraID: "customfield_2", WindshiftType: "text", Action: "create"},
				{JiraID: "customfield_1", WindshiftType: "number", Action: "create"},
			},
		},
		Xray: XrayImportOptions{
			ImportTests:      true,
			Region:           "global",
			ClientID:         "secret-client",
			ClientSecret:     "secret-value",
			TestIssueTypeIDs: []string{"2", "1"},
		},
	}
	right := left
	right.ProjectKeys = []string{"kan", "SP"}
	right.ConnectionID = "connection-b"
	right.ForceReimport = true
	right.Mappings.Workspaces = []WorkspaceMapping{
		{JiraKey: "KAN", IssueCount: 999, KeyAliasAcknowledged: true, CreateNew: true, NewWorkspaceKey: "KAN"},
		{JiraKey: "SP", CreateNew: true, NewWorkspaceKey: "SP"},
	}
	right.Mappings.CustomFields = []CustomFieldMapping{
		{JiraID: "customfield_1", WindshiftType: "number", Action: "create"},
		{JiraID: "customfield_2", WindshiftType: "text", Action: "create"},
	}
	right.Xray.ClientID = "different-secret-client"
	right.Xray.ClientSecret = "different-secret-value"
	right.Xray.TestIssueTypeIDs = []string{"1", "2"}

	leftFingerprint, err := jiraImportPlanFingerprint(left)
	if err != nil {
		t.Fatalf("left fingerprint: %v", err)
	}
	rightFingerprint, err := jiraImportPlanFingerprint(right)
	if err != nil {
		t.Fatalf("right fingerprint: %v", err)
	}
	if leftFingerprint != rightFingerprint {
		t.Fatalf("stable fingerprints differ: left=%s right=%s", leftFingerprint, rightFingerprint)
	}

	right.Mappings.CustomFields[0].Action = "skip"
	changedFingerprint, err := jiraImportPlanFingerprint(right)
	if err != nil {
		t.Fatalf("changed fingerprint: %v", err)
	}
	if changedFingerprint == leftFingerprint {
		t.Fatalf("mapping change did not alter fingerprint %s", changedFingerprint)
	}
}

func TestUniqueImportUsernameHandlesCaseInsensitiveCollisions(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-usernames.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('first@example.invalid', 'Shared', 'First', 'User', false),
		       ('second@example.invalid', 'shared-2', 'Second', 'User', false)
	`); err != nil {
		t.Fatalf("insert colliding users: %v", err)
	}

	h := NewJiraImportHandler(db, "jira-import-username-test-secret-32", "", blobstore.Disabled())
	got, err := h.imports.UniqueImportedUsername("shared")
	if err != nil {
		t.Fatalf("allocate unique import username: %v", err)
	}
	if got != "shared-3" {
		t.Fatalf("unique import username = %q, want %q", got, "shared-3")
	}
}

func TestJiraBoardSupportsSprintsOnlyForScrumBoards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		board jira.JiraBoard
		want  bool
	}{
		{name: "scrum", board: jira.JiraBoard{Type: "scrum"}, want: true},
		{name: "scrum ignores case and whitespace", board: jira.JiraBoard{Type: " Scrum "}, want: true},
		{name: "kanban", board: jira.JiraBoard{Type: "kanban"}, want: false},
		{name: "unknown", board: jira.JiraBoard{}, want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := jiraBoardSupportsSprints(test.board); got != test.want {
				t.Fatalf("jiraBoardSupportsSprints(%q) = %t, want %t", test.board.Type, got, test.want)
			}
		})
	}
}

func TestFindConflictingJiraImports(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-conflicts.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}

	insertJiraImportConnection(t, db, "conn-1")
	insertJiraImportConnection(t, db, "conn-2")
	insertJiraImportJob(t, db, "job-completed", "conn-1", "completed", []string{"ASFJ", "BILL"})
	insertJiraImportJob(t, db, "job-deleted", "conn-1", "data_deleted", []string{"CRM"})
	insertJiraImportJob(t, db, "job-other-connection", "conn-2", "completed", []string{"ASFJ"})

	h := NewJiraImportHandler(db, "jira-import-conflict-test-secret-32-bytes", "", blobstore.Disabled())
	jobRepository := repository.NewJiraImportJobRepository(h.db)
	conflicts, err := findConflictingJiraImportsWithQuery(jobRepository, StartImportRequest{
		ConnectionID: "conn-1",
		ProjectKeys:  []string{" bill "},
	})
	if err != nil {
		t.Fatalf("find conflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("conflict count = %d, want 1 (%#v)", len(conflicts), conflicts)
	}
	if conflicts[0].JobID != "job-completed" {
		t.Fatalf("conflict job = %q, want job-completed", conflicts[0].JobID)
	}
	if !conflicts[0].ConfigurationDrift {
		t.Fatal("partial project scope should be reported as configuration drift")
	}
	if got, want := conflicts[0].ProjectKeys, []string{"ASFJ", "BILL"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("conflict project keys = %#v, want %#v", got, want)
	}

	conflicts, err = findConflictingJiraImportsWithQuery(jobRepository, StartImportRequest{
		ConnectionID: "conn-1",
		ProjectKeys:  []string{"CRM"},
	})
	if err != nil {
		t.Fatalf("find deleted-only conflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("deleted import conflicts = %#v, want none", conflicts)
	}

	conflicts, err = findConflictingJiraImportsWithQuery(jobRepository, StartImportRequest{
		ConnectionID: "conn-1",
		ProjectKeys:  []string{"ASFJ", "BILL"},
	})
	if err != nil {
		t.Fatalf("find exact-plan conflicts: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].ConfigurationDrift {
		t.Fatalf("exact-plan conflicts = %#v, want one without configuration drift", conflicts)
	}
}

func TestDeleteImportedDataRequiresStrongConfirmation(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-delete-guard.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}

	insertJiraImportConnection(t, db, "conn-1")
	insertJiraImportJob(t, db, "job-delete", "conn-1", "completed", []string{"ASFJ"})
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_id_mappings (job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
		VALUES ('job-delete', 'workspace', 'ASFJ', 'ASFJ', 12345, '{"was_created":true}')
	`); err != nil {
		t.Fatalf("insert workspace mapping: %v", err)
	}

	h := NewJiraImportHandler(db, "jira-import-delete-guard-test-secret-32", "", blobstore.Disabled())

	mismatchBody := `{"confirm_job_id":"job-delete","confirm_workspace_count":0,"confirm_delete_imported_data":true}`
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/jira-import/jobs/job-delete/data", strings.NewReader(mismatchBody))
	req.SetPathValue("jobId", "job-delete")
	w := httptest.NewRecorder()
	h.DeleteImportedData(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("count mismatch status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	matchingBody := bytes.NewBufferString(`{"confirm_job_id":"job-delete","confirm_workspace_count":1,"confirm_delete_imported_data":true}`)
	req = httptest.NewRequest(http.MethodDelete, "/api/admin/jira-import/jobs/job-delete/data", matchingBody)
	req.SetPathValue("jobId", "wrong-job")
	w = httptest.NewRecorder()
	h.DeleteImportedData(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("job-id mismatch status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestEnsureCustomFieldsCreatesJiraAssetFieldForResolvedFieldSchema(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-asset-field.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}

	insertJiraImportConnection(t, db, "conn-1")
	insertJiraImportJob(t, db, "job-asset-field", "conn-1", "running", []string{"ASFJ"})
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_id_mappings (job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
		VALUES ('job-asset-field', 'asset_set', 'schema-1', 'ITSM', 77, '{}')
	`); err != nil {
		t.Fatalf("insert asset set mapping: %v", err)
	}

	h := NewJiraImportHandler(db, "jira-import-asset-field-test-secret", "", blobstore.Disabled())
	fieldMap, _, err := h.ensureCustomFields(t.Context(), "job-asset-field", []CustomFieldMapping{{
		JiraID:        "customfield_12345",
		JiraName:      "Affected hardware",
		JiraType:      "com.atlassian.jira.plugins.jira-servicedesk-cmdb-plugin:insight-object-field",
		WindshiftType: "asset",
		Action:        "create",
	}}, map[string]int{"customfield_12345": 77}, nil)
	if err != nil {
		t.Fatalf("ensure custom fields: %v", err)
	}
	fieldID := fieldMap["customfield_12345"]
	if fieldID == 0 {
		t.Fatalf("asset field not mapped: %#v", fieldMap)
	}
	var fieldType, options string
	if err := db.QueryRow(`SELECT field_type, options FROM custom_field_definitions WHERE id = ?`, fieldID).Scan(&fieldType, &options); err != nil {
		t.Fatalf("load custom field: %v", err)
	}
	if fieldType != "asset" {
		t.Fatalf("field_type = %q, want asset", fieldType)
	}
	if !strings.Contains(options, `"asset_set_id":77`) {
		t.Fatalf("options = %s, want asset_set_id 77", options)
	}
}

func TestEnsureCustomFieldsCreatesStableObservedChoiceOptions(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-choice-fields.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-choice-fields")
	insertJiraImportJob(t, db, "job-choice-fields", "conn-choice-fields", "running", []string{"APP"})

	h := NewJiraImportHandler(db, "jira-import-choice-field-secret", "", blobstore.Disabled())
	mappings := []CustomFieldMapping{{
		JiraID:        "customfield_20001",
		JiraName:      "Environment",
		JiraType:      "vendor.app:environment",
		WindshiftType: "select",
		Action:        "create",
	}}
	fieldMap, optionIDs, err := h.ensureCustomFields(
		t.Context(),
		"job-choice-fields",
		mappings,
		nil,
		map[string][]string{"customfield_20001": {"Red", "Blue"}},
	)
	if err != nil {
		t.Fatalf("ensure custom fields: %v", err)
	}
	fieldID := fieldMap["customfield_20001"]
	if fieldID == 0 {
		t.Fatalf("choice field not created: %#v", fieldMap)
	}
	firstBlue := optionIDs["customfield_20001"]["blue"]
	firstRed := optionIDs["customfield_20001"]["red"]
	if firstBlue <= 0 || firstRed <= 0 || firstBlue == firstRed {
		t.Fatalf("option IDs = %#v, want distinct positive IDs", optionIDs)
	}

	_, optionIDs, err = h.ensureCustomFields(
		t.Context(),
		"job-choice-fields",
		mappings,
		nil,
		map[string][]string{"customfield_20001": {"red", "Green"}},
	)
	if err != nil {
		t.Fatalf("ensure custom fields again: %v", err)
	}
	if optionIDs["customfield_20001"]["red"] != firstRed {
		t.Fatalf("Red option ID changed from %d to %d", firstRed, optionIDs["customfield_20001"]["red"])
	}
	if optionIDs["customfield_20001"]["green"] <= 0 {
		t.Fatalf("Green option missing: %#v", optionIDs)
	}

	var rawOptions string
	if err := db.QueryRow(`SELECT options FROM custom_field_definitions WHERE id = ?`, fieldID).Scan(&rawOptions); err != nil {
		t.Fatalf("load choice options: %v", err)
	}
	var options jiraImportCustomFieldOptions
	if err := json.Unmarshal([]byte(rawOptions), &options); err != nil {
		t.Fatalf("decode choice options: %v", err)
	}
	if len(options.Items) != 3 {
		t.Fatalf("choice options = %#v, want Blue, Red, and Green", options.Items)
	}
}

func TestEnsureCustomFieldsKeepsSameNameJiraSourcesDistinct(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-same-name-fields.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-same-name-fields")
	insertJiraImportJob(t, db, "job-same-name-fields", "conn-same-name-fields", "running", []string{"APP"})

	h := NewJiraImportHandler(db, "jira-import-same-name-field-secret", "", blobstore.Disabled())
	mappings := []CustomFieldMapping{
		{
			JiraID: "customfield_21001", JiraName: "Persona",
			JiraType: "vendor.one:persona", WindshiftType: "text", Action: "create",
		},
		{
			JiraID: "customfield_21002", JiraName: "Persona",
			JiraType: "vendor.two:persona", WindshiftType: "text", Action: "create",
		},
	}
	fieldMap, _, err := h.ensureCustomFields(t.Context(), "job-same-name-fields", mappings, nil, nil)
	if err != nil {
		t.Fatalf("ensure custom fields: %v", err)
	}
	if fieldMap["customfield_21001"] == 0 || fieldMap["customfield_21002"] == 0 ||
		fieldMap["customfield_21001"] == fieldMap["customfield_21002"] {
		t.Fatalf("same-name fields were conflated: %#v", fieldMap)
	}

	reimported, _, err := h.ensureCustomFields(t.Context(), "job-same-name-fields", mappings, nil, nil)
	if err != nil {
		t.Fatalf("ensure custom fields again: %v", err)
	}
	if reimported["customfield_21001"] != fieldMap["customfield_21001"] ||
		reimported["customfield_21002"] != fieldMap["customfield_21002"] {
		t.Fatalf("source-specific field IDs changed: first=%#v second=%#v", fieldMap, reimported)
	}
}

func TestEnsureCustomFieldsMapsDifferentJiraAssetFieldsToDifferentSets(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-multiple-asset-fields.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-multiple-assets")
	insertJiraImportJob(t, db, "job-asset-fields", "conn-multiple-assets", "running", []string{"SP"})

	h := NewJiraImportHandler(db, "jira-import-multiple-asset-fields-secret", "", blobstore.Disabled())
	fieldMap, _, err := h.ensureCustomFields(t.Context(), "job-asset-fields", []CustomFieldMapping{
		{
			JiraID:        "customfield_10040",
			JiraName:      "Affected hardware",
			JiraType:      "com.atlassian.jira.plugins.cmdb:cmdb-object-cftype",
			WindshiftType: "asset",
			Action:        "create",
		},
		{
			JiraID: "customfield_10041",
			// Jira permits distinct custom fields with the same display name.
			// They must not share a Windshift definition when their Assets
			// schemas differ.
			JiraName:      "Affected hardware",
			JiraType:      "com.atlassian.jira.plugins.cmdb:cmdb-object-cftype",
			WindshiftType: "asset",
			Action:        "create",
		},
	}, map[string]int{
		"customfield_10040": 77,
		"customfield_10041": 88,
	}, nil)
	if err != nil {
		t.Fatalf("ensure custom fields: %v", err)
	}

	for jiraFieldID, wantSetID := range map[string]int{
		"customfield_10040": 77,
		"customfield_10041": 88,
	} {
		fieldID := fieldMap[jiraFieldID]
		if fieldID == 0 {
			t.Fatalf("%s was not mapped: %#v", jiraFieldID, fieldMap)
		}
		var fieldType, options string
		if err := db.QueryRow(`SELECT field_type, options FROM custom_field_definitions WHERE id = ?`, fieldID).Scan(&fieldType, &options); err != nil {
			t.Fatalf("load %s: %v", jiraFieldID, err)
		}
		if fieldType != "asset" {
			t.Fatalf("%s field_type = %q, want asset", jiraFieldID, fieldType)
		}
		wantOption := fmt.Sprintf(`"asset_set_id":%d`, wantSetID)
		if !strings.Contains(options, wantOption) {
			t.Fatalf("%s options = %s, want %s", jiraFieldID, options, wantOption)
		}
	}
	if fieldMap["customfield_10040"] == fieldMap["customfield_10041"] {
		t.Fatalf("different asset sets reused one Windshift field: %#v", fieldMap)
	}
}

func TestEnsureCustomFieldsPreservesUnresolvedJiraAssetFieldAsText(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-unresolved-asset-field.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-unresolved-asset")
	insertJiraImportJob(t, db, "job-unresolved-asset", "conn-unresolved-asset", "running", []string{"SP"})

	h := NewJiraImportHandler(db, "jira-import-unresolved-asset-field-secret", "", blobstore.Disabled())
	fieldMap, _, err := h.ensureCustomFields(t.Context(), "job-unresolved-asset", []CustomFieldMapping{{
		JiraID:        "customfield_10040",
		JiraName:      "Affected hardware",
		JiraType:      "com.atlassian.jira.plugins.cmdb:cmdb-object-cftype",
		WindshiftType: "asset",
		Action:        "create",
	}}, nil, nil)
	if err != nil {
		t.Fatalf("ensure custom fields: %v", err)
	}
	var fieldType string
	if err := db.QueryRow(`SELECT field_type FROM custom_field_definitions WHERE id = ?`, fieldMap["customfield_10040"]).Scan(&fieldType); err != nil {
		t.Fatalf("load field: %v", err)
	}
	if fieldType != "textarea" {
		t.Fatalf("field_type = %q, want textarea", fieldType)
	}
}

func TestBindJiraImportFieldsCreatesWorkspaceScopedScreens(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-asset-screens.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-jira-asset-screen-test")
	insertJiraImportJob(t, db, "jira-asset-screen-test", "conn-jira-asset-screen-test", "running", []string{"SP"})

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('Imported Service Project', 'SP', '', true) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var configSetID int
	if err := db.QueryRow(`
		INSERT INTO configuration_sets (name, description)
		VALUES ('SP Configuration', '') RETURNING id
	`).Scan(&configSetID); err != nil {
		t.Fatalf("insert configuration set: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id)
		VALUES (?, ?)
	`, workspaceID, configSetID); err != nil {
		t.Fatalf("assign configuration set: %v", err)
	}
	var assetFieldID int
	if err := db.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type, options)
		VALUES ('Affected hardware', 'asset', '{"asset_set_id":77,"multi":true}') RETURNING id
	`).Scan(&assetFieldID); err != nil {
		t.Fatalf("insert asset field: %v", err)
	}
	var jiraKeyFieldID int
	if err := db.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type, options)
		VALUES ('Jira Key', 'text', '') RETURNING id
	`).Scan(&jiraKeyFieldID); err != nil {
		t.Fatalf("insert Jira Key field: %v", err)
	}

	var originalDefaultCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM screen_fields WHERE screen_id = 1`).Scan(&originalDefaultCount); err != nil {
		t.Fatalf("count default screen fields: %v", err)
	}

	h := NewJiraImportHandler(db, "jira-import-asset-screen-test-secret", "", blobstore.Disabled())
	if err := h.bindJiraImportFieldsToWorkspace(
		workspaceID,
		"SP",
		map[string]int{
			"customfield_10040":       assetFieldID,
			jiraIssueKeyFieldSourceID: jiraKeyFieldID,
		},
		[]CustomFieldMapping{{
			JiraID:        "customfield_10040",
			WindshiftType: "asset",
			Action:        "create",
		}},
		map[string]bool{"customfield_10040": true},
	); err != nil {
		t.Fatalf("bind Jira asset fields: %v", err)
	}

	var assignmentCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM configuration_set_screens WHERE configuration_set_id = ?
	`, configSetID).Scan(&assignmentCount); err != nil {
		t.Fatalf("count screen assignments: %v", err)
	}
	if assignmentCount != 3 {
		t.Fatalf("screen assignment count = %d, want 3", assignmentCount)
	}

	var mappedScreenCount int
	if err := db.QueryRow(`
		SELECT COUNT(DISTINCT css.screen_id)
		FROM configuration_set_screens css
		JOIN screen_fields sf ON sf.screen_id = css.screen_id
		WHERE css.configuration_set_id = ?
		  AND sf.field_type = 'custom'
		  AND sf.field_identifier = ?
	`, configSetID, fmt.Sprint(assetFieldID)).Scan(&mappedScreenCount); err != nil {
		t.Fatalf("count mapped screens: %v", err)
	}
	if mappedScreenCount != 3 {
		t.Fatalf("screens containing asset field = %d, want 3", mappedScreenCount)
	}
	if err := db.QueryRow(`
		SELECT COUNT(DISTINCT css.screen_id)
		FROM configuration_set_screens css
		JOIN screen_fields sf ON sf.screen_id = css.screen_id
		WHERE css.configuration_set_id = ?
		  AND sf.field_type = 'custom'
		  AND sf.field_identifier = ?
	`, configSetID, fmt.Sprint(jiraKeyFieldID)).Scan(&mappedScreenCount); err != nil {
		t.Fatalf("count screens containing Jira Key: %v", err)
	}
	if mappedScreenCount != 3 {
		t.Fatalf("screens containing Jira Key = %d, want 3", mappedScreenCount)
	}

	var finalDefaultCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM screen_fields WHERE screen_id = 1`).Scan(&finalDefaultCount); err != nil {
		t.Fatalf("recount default screen fields: %v", err)
	}
	if finalDefaultCount != originalDefaultCount {
		t.Fatalf("shared default screen changed from %d to %d fields", originalDefaultCount, finalDefaultCount)
	}
}

func TestEnsureAffectsVersionCustomFieldStoresAllVersionsAsMultiselect(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-affects-version.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}

	insertJiraImportConnection(t, db, "conn-1")
	insertJiraImportJob(t, db, "job-affects", "conn-1", "running", []string{"ASFJ"})

	h := NewJiraImportHandler(db, "jira-import-affects-test-secret-32", "", blobstore.Disabled())
	field, err := h.ensureAffectsVersionCustomField(t.Context(), "job-affects", []VersionMapping{
		{JiraID: "10000", JiraName: "1.0"},
		{JiraID: "10001", JiraName: "2.0"},
		{JiraID: "10002", JiraName: "1.0"},
	})
	if err != nil {
		t.Fatalf("ensureAffectsVersionCustomField: %v", err)
	}
	if field == nil {
		t.Fatal("field is nil")
	}
	if got := field.OptionIDsByJiraID["10000"]; got == 0 {
		t.Fatalf("version 10000 option ID missing: %#v", field.OptionIDsByJiraID)
	}
	if got, want := field.OptionIDsByJiraID["10002"], field.OptionIDsByJiraID["10000"]; got != want {
		t.Fatalf("duplicate version name option ID = %d, want shared ID %d", got, want)
	}

	var fieldType, options string
	if err := db.QueryRow(`SELECT field_type, options FROM custom_field_definitions WHERE id = ?`, field.FieldID).Scan(&fieldType, &options); err != nil {
		t.Fatalf("load custom field: %v", err)
	}
	if fieldType != "multiselect" {
		t.Fatalf("field_type = %q, want multiselect", fieldType)
	}
	if !strings.Contains(options, `"label":"1.0"`) || !strings.Contains(options, `"label":"2.0"`) {
		t.Fatalf("options do not contain affected version labels: %s", options)
	}
}

func TestDeleteImportedDataRemovesAttachmentFiles(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-delete-attachments.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}

	root := t.TempDir()
	filePath := filepath.Join(root, "jira-attachment.txt")
	if err := os.WriteFile(filePath, []byte("jira attachment"), 0o600); err != nil {
		t.Fatalf("write attachment file: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO attachment_settings (max_file_size, allowed_mime_types, attachment_path, enabled)
		VALUES (52428800, '', ?, true)
	`, root); err != nil {
		t.Fatalf("configure attachment settings: %v", err)
	}

	insertJiraImportConnection(t, db, "conn-1")
	insertJiraImportJob(t, db, "job-attachments", "conn-1", "completed", []string{"ASFJ"})
	var attachmentID int
	if err := db.QueryRow(`
		INSERT INTO attachments (item_id, entity_type, filename, original_filename, file_path, mime_type, file_size)
		VALUES (1, 'item', 'jira-attachment.txt', 'jira-attachment.txt', ?, 'text/plain', 15) RETURNING id
	`, filePath).Scan(&attachmentID); err != nil {
		t.Fatalf("insert attachment row: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_id_mappings (job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
		VALUES ('job-attachments', 'attachment', 'att-1', 'ASFJ-1', ?, '{"was_created":true}')
	`, attachmentID); err != nil {
		t.Fatalf("insert attachment mapping: %v", err)
	}

	h := NewJiraImportHandler(db, "jira-import-delete-file-test-secret-32", "", blobstore.NewLocal(root))
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/jira-import/jobs/job-attachments/data", bytes.NewBufferString(`{"confirm_job_id":"job-attachments","confirm_workspace_count":0,"confirm_delete_imported_data":true}`))
	req.SetPathValue("jobId", "job-attachments")
	w := httptest.NewRecorder()
	h.DeleteImportedData(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete imported data status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("attachment file still exists or stat failed unexpectedly: %v", err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id = ?`, attachmentID).Scan(&remaining); err != nil {
		t.Fatalf("count attachment rows: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("attachment rows remaining = %d, want 0", remaining)
	}
}

func insertJiraImportConnection(t *testing.T, db database.Database, id string) {
	t.Helper()
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_connections (id, instance_url, email, encrypted_credentials, instance_name, deployment_type)
		VALUES (?, 'https://example.invalid', 'redacted@example.invalid', '{}', 'Fixture Jira', 'cloud')
	`, id); err != nil {
		t.Fatalf("insert jira connection %s: %v", id, err)
	}
}

// claimJiraImportJobForTest marks a running fixture job as owned by the handler
// under test so its fenced job-state writes apply.
func claimJiraImportJobForTest(t *testing.T, handler *JiraImportHandler, db database.Database, jobID string) {
	t.Helper()
	token := "test-worker-token"
	if _, err := db.ExecWrite(`UPDATE jira_import_jobs SET lease_token = ? WHERE id = ?`, token, jobID); err != nil {
		t.Fatalf("claim job %s: %v", jobID, err)
	}
	handler.setJobToken(jobID, token)
}

func insertJiraImportJob(t *testing.T, db database.Database, id, connectionID, status string, projectKeys []string) {
	t.Helper()
	configBytes, err := json.Marshal(map[string]interface{}{"project_keys": projectKeys})
	if err != nil {
		t.Fatalf("marshal jira import job config: %v", err)
	}
	// Active fixtures represent a live worker, so they carry a fresh lease.
	// Tests that exercise interruption overwrite this with an expired value.
	var lease any
	if status == "queued" || status == "running" {
		lease = time.Now().Add(2 * time.Minute).Unix()
	}
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_jobs (id, connection_id, status, scope, config_json, created_at, completed_at, lease_expires_at)
		VALUES (?, ?, ?, 'work_items', ?, ?, ?, ?)
	`, id, connectionID, status, string(configBytes), time.Now(), time.Now(), lease); err != nil {
		t.Fatalf("insert jira import job %s: %v", id, err)
	}
}

func TestTranslateJQLToWindshiftQLPreservesUnsupportedClauses(t *testing.T) {
	ql, unsupported := jira.TranslateJQLToWindshiftQL(`project = ASFJ AND status IN ("To Do", Done) AND priority = Highest AND component = API ORDER BY created DESC`, 42)
	for _, want := range []string{
		`workspace_id = 42`,
		`status IN ("To Do", "Done")`,
		`priority = "Critical"`,
		`labels = "component:API"`,
	} {
		if !strings.Contains(ql, want) {
			t.Fatalf("ql = %q, want to contain %q", ql, want)
		}
	}
	if len(unsupported) != 1 || !strings.Contains(unsupported[0], "ORDER BY") {
		t.Fatalf("unsupported = %#v, want ORDER BY", unsupported)
	}
}

func TestJiraBoardColumnsMapsStatusesAndBacklog(t *testing.T) {
	h := &JiraImportHandler{}
	columns, backlog, unsupported := h.jiraBoardColumns(&jira.JiraBoardConfiguration{ColumnConfig: &jira.JiraBoardColumnConfig{Columns: []jira.JiraBoardConfigColumn{
		{Name: "Backlog", Statuses: []jira.JiraBoardColumnStatus{{ID: "10"}}},
		{Name: "Doing", Statuses: []jira.JiraBoardColumnStatus{{ID: "20"}, {ID: "missing"}}},
	}}}, map[string]int{"10": 1, "20": 2})
	if got, want := fmt.Sprint(backlog), "[1]"; got != want {
		t.Fatalf("backlog = %s, want %s", got, want)
	}
	if len(columns) != 1 || columns[0].Name != "Doing" || fmt.Sprint(columns[0].StatusIDs) != "[2]" {
		t.Fatalf("columns = %#v, want Doing with status 2", columns)
	}
	if got, want := fmt.Sprint(unsupported), "[missing]"; got != want {
		t.Fatalf("unsupported = %s, want %s", got, want)
	}
}

func TestEnsureJiraBoardConfigurationCreatesRowsAndMapping(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-import-board-config.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-1")
	insertJiraImportJob(t, db, "job-board", "conn-1", "running", []string{"ASFJ"})

	var collectionID int
	if err := db.QueryRow(`INSERT INTO collections (name, ql_query) VALUES ('Jira Board: Team', 'workspace_id = 1') RETURNING id`).Scan(&collectionID); err != nil {
		t.Fatalf("insert collection: %v", err)
	}

	h := NewJiraImportHandler(db, "jira-import-board-test-secret", "", blobstore.Disabled())
	configID, ok := h.ensureJiraBoardConfiguration("job-board", jira.JiraBoard{ID: 77, Name: "Team", Type: "scrum"}, collectionID, []models.BoardColumnRequest{
		{Name: "To Do", StatusIDs: []int{1}, Color: "#64748b"},
		{Name: "Done", StatusIDs: []int{3}, Color: "#22c55e"},
	}, []int{2}, nil)
	if !ok || configID == 0 {
		t.Fatalf("ensureJiraBoardConfiguration = (%d, %v), want created", configID, ok)
	}
	var columnCount, statusCount, mappingCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM board_columns WHERE board_configuration_id = ?`, configID).Scan(&columnCount); err != nil {
		t.Fatalf("count columns: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM board_column_statuses bcs JOIN board_columns bc ON bc.id = bcs.board_column_id WHERE bc.board_configuration_id = ?`, configID).Scan(&statusCount); err != nil {
		t.Fatalf("count statuses: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM jira_import_id_mappings WHERE job_id = 'job-board' AND entity_type = 'board_configuration' AND jira_id = 'board:77' AND windshift_id = ?`, configID).Scan(&mappingCount); err != nil {
		t.Fatalf("count mapping: %v", err)
	}
	if columnCount != 2 || statusCount != 2 || mappingCount != 1 {
		t.Fatalf("columnCount=%d statusCount=%d mappingCount=%d, want 2/2/1", columnCount, statusCount, mappingCount)
	}
}
