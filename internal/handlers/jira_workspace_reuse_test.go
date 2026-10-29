package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestValidateJiraWorkspaceMappingsAllowsExistingWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Existing target', 'EXST') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-workspace-reuse-test-secret", "", blobstore.Disabled())
	req := StartImportRequest{
		ProjectKeys: []string{"APP"},
		Mappings: ImportMappings{
			Workspaces: []WorkspaceMapping{{
				JiraKey:     "APP",
				JiraName:    "Application",
				CreateNew:   false,
				WindshiftID: &workspaceID,
			}},
		},
	}
	if err := handler.validateJiraWorkspaceMappings(req); err != nil {
		t.Fatalf("validateJiraWorkspaceMappings() = %v, want nil", err)
	}
}

func TestEnsureWorkspaceReusesExistingWorkspaceWithoutClaimingOwnership(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-reuse")
	insertJiraImportJob(t, tdb.Database, "job-reuse", "connection-reuse", "running", []string{"APP"})

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Existing target', 'EXST') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-workspace-reuse-ensure-secret", "", blobstore.Disabled())
	mapping := &WorkspaceMapping{
		JiraKey:     "APP",
		JiraName:    "Application",
		CreateNew:   false,
		WindshiftID: &workspaceID,
	}
	gotID, err := handler.ensureWorkspace(context.Background(), "job-reuse", mapping, 0)
	if err != nil {
		t.Fatalf("ensureWorkspace() = %v", err)
	}
	if gotID != workspaceID {
		t.Fatalf("ensureWorkspace() = %d, want existing workspace %d", gotID, workspaceID)
	}
	if mapping.NewWorkspaceName != "Existing target" || mapping.NewWorkspaceKey != "EXST" {
		t.Fatalf("mapping backfill = %q/%q, want existing workspace name/key", mapping.NewWorkspaceName, mapping.NewWorkspaceKey)
	}

	var name, key string
	if err := tdb.QueryRow(`SELECT name, key FROM workspaces WHERE id = ?`, workspaceID).Scan(&name, &key); err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if name != "Existing target" || key != "EXST" {
		t.Fatalf("reused workspace mutated to %q/%q", name, key)
	}

	var metadata sql.NullString
	if err := tdb.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-reuse' AND entity_type = 'workspace' AND jira_id = 'APP'
	`).Scan(&metadata); err != nil {
		t.Fatalf("load workspace mapping: %v", err)
	}
	if jiraImportMappingWasCreated(metadata) {
		t.Fatalf("reused workspace mapping claims ownership: %s", metadata.String)
	}
	if action := jiraImportMappingMetadata(metadata)["action"]; action != "reuse_existing" {
		t.Fatalf("reused workspace mapping action = %v, want reuse_existing", action)
	}
}

func TestValidateJiraWorkspaceMappingsRejectsUnknownWorkspaceTarget(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	missingID := 987654
	handler := NewJiraImportHandler(tdb.Database, "jira-workspace-missing-target-secret", "", blobstore.Disabled())
	req := StartImportRequest{
		ProjectKeys: []string{"APP"},
		Mappings: ImportMappings{
			Workspaces: []WorkspaceMapping{{
				JiraKey: "APP", JiraName: "Application", CreateNew: false, WindshiftID: &missingID,
			}},
		},
	}
	err := handler.validateJiraWorkspaceMappings(req)
	if err == nil || !strings.Contains(err.Error(), "cannot be imported into") {
		t.Fatalf("validateJiraWorkspaceMappings() = %v, want unusable-workspace error", err)
	}
}

func TestValidateJiraWorkspaceMappingsRejectsDuplicateWorkspaceTarget(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Shared target', 'SHRD') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-workspace-duplicate-target-secret", "", blobstore.Disabled())
	req := StartImportRequest{
		ProjectKeys: []string{"APP", "OPS"},
		Mappings: ImportMappings{
			Workspaces: []WorkspaceMapping{
				{JiraKey: "APP", JiraName: "Application", CreateNew: false, WindshiftID: &workspaceID},
				{JiraKey: "OPS", JiraName: "Operations", CreateNew: false, WindshiftID: &workspaceID},
			},
		},
	}
	err := handler.validateJiraWorkspaceMappings(req)
	if err == nil || !strings.Contains(err.Error(), "same workspace") {
		t.Fatalf("validateJiraWorkspaceMappings() = %v, want duplicate-workspace error", err)
	}
}

func TestStartJiraImportRejectsWorkspaceReuseWithoutTarget(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-reuse-missing")

	handler := NewJiraImportHandler(tdb.Database, "jira-workspace-reuse-start-secret", "", blobstore.Disabled())
	body := `{
		"connection_id":"connection-reuse-missing",
		"project_keys":["APP"],
		"mappings":{"workspaces":[{
			"jiraKey":"APP","jiraName":"Application","createNew":false
		}]},
		"xray":{"import_tests":false}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/jira-import/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.StartImport(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("StartImport() status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "target workspace") {
		t.Fatalf("StartImport() body = %s, want missing-target explanation", w.Body.String())
	}

	var jobs int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM jira_import_jobs`).Scan(&jobs); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobs != 0 {
		t.Fatalf("jobs = %d, want 0 after rejected workspace reuse", jobs)
	}
}

func TestValidateJiraEntityMappingsRejectsMissingTargets(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	missingID := 987654
	handler := NewJiraImportHandler(tdb.Database, "jira-entity-mapping-missing-secret", "", blobstore.Disabled())

	tests := []struct {
		name    string
		request StartImportRequest
		want    string
	}{
		{
			name: "status",
			request: StartImportRequest{Mappings: ImportMappings{
				Statuses: []StatusMapping{{JiraName: "Done", CreateNew: false, WindshiftID: &missingID}},
			}},
			want: "status that does not exist",
		},
		{
			name: "item type",
			request: StartImportRequest{Mappings: ImportMappings{
				IssueTypes: []IssueTypeMapping{{JiraName: "Story", CreateNew: false, WindshiftID: &missingID}},
			}},
			want: "item type that does not exist",
		},
		{
			name: "custom field",
			request: StartImportRequest{Mappings: ImportMappings{
				CustomFields: []CustomFieldMapping{{JiraName: "Environment", Action: "map", WindshiftID: &missingID}},
			}},
			want: "custom field that does not exist",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := handler.validateJiraEntityMappings(test.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateJiraEntityMappings() = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateJiraEntityMappingsAcceptsExistingTargets(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	var statusID, itemTypeID, customFieldID int
	if err := tdb.QueryRow(`INSERT INTO statuses (name, category_id) VALUES ('Mapped status', 1) RETURNING id`).Scan(&statusID); err != nil {
		t.Fatalf("create status: %v", err)
	}
	if err := tdb.QueryRow(`INSERT INTO item_types (name) VALUES ('Mapped item type') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("create item type: %v", err)
	}
	if err := tdb.QueryRow(`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Mapped field', 'text') RETURNING id`).Scan(&customFieldID); err != nil {
		t.Fatalf("create custom field: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-entity-mapping-valid-secret", "", blobstore.Disabled())
	req := StartImportRequest{Mappings: ImportMappings{
		Statuses:     []StatusMapping{{JiraName: "Done", CreateNew: false, WindshiftID: &statusID}},
		IssueTypes:   []IssueTypeMapping{{JiraName: "Story", CreateNew: false, WindshiftID: &itemTypeID}},
		CustomFields: []CustomFieldMapping{{JiraName: "Environment", Action: "map", WindshiftID: &customFieldID}},
	}}
	if err := handler.validateJiraEntityMappings(req); err != nil {
		t.Fatalf("validateJiraEntityMappings() = %v, want nil", err)
	}
}

// A Jira field whose extracted value shape does not fit the existing custom
// field must be rejected rather than silently writing the wrong type.
func TestValidateJiraEntityMappingsRejectsIncompatibleCustomField(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	var textFieldID int
	if err := tdb.QueryRow(`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Mapped text', 'text') RETURNING id`).Scan(&textFieldID); err != nil {
		t.Fatalf("create custom field: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-entity-mapping-mismatch-secret", "", blobstore.Disabled())
	req := StartImportRequest{Mappings: ImportMappings{
		CustomFields: []CustomFieldMapping{{
			JiraName: "Story points", Action: "map", WindshiftID: &textFieldID, WindshiftType: "number",
		}},
	}}
	err := handler.validateJiraEntityMappings(req)
	if err == nil || !strings.Contains(err.Error(), "cannot map") {
		t.Fatalf("validateJiraEntityMappings() = %v, want incompatible-type error", err)
	}
}
