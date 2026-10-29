package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
)

type jiraCustomFieldsFixture struct {
	ProjectKey      string                        `json:"project_key"`
	FieldCount      int                           `json:"field_count"`
	SuggestionCount int                           `json:"suggestion_count"`
	Fields          []jira.JiraCustomField        `json:"fields"`
	Suggestions     []jira.FieldMappingSuggestion `json:"suggestions"`
}

func TestJiraCustomFieldDefinitionsFromCapturedFixture(t *testing.T) {
	fixtureBytes, err := os.ReadFile("testdata/jira_custom_fields_ASFJ.json")
	if err != nil {
		t.Fatalf("read Jira custom fields fixture: %v", err)
	}
	var fx jiraCustomFieldsFixture
	if err := json.Unmarshal(fixtureBytes, &fx); err != nil {
		t.Fatalf("decode Jira custom fields fixture: %v", err)
	}
	if len(fx.Fields) != fx.FieldCount {
		t.Fatalf("fixture field_count=%d but has %d fields", fx.FieldCount, len(fx.Fields))
	}
	if len(fx.Suggestions) != fx.SuggestionCount {
		t.Fatalf("fixture suggestion_count=%d but has %d suggestions", fx.SuggestionCount, len(fx.Suggestions))
	}

	suggestions := jira.SuggestFieldMappings(fx.Fields)
	if len(suggestions) != fx.FieldCount {
		t.Fatalf("current mapper produced %d suggestions from %d fixture fields", len(suggestions), fx.FieldCount)
	}

	mappings := make([]CustomFieldMapping, 0, len(suggestions))
	suggestionsByID := make(map[string]jira.FieldMappingSuggestion, len(suggestions))
	for _, s := range suggestions {
		suggestionsByID[s.JiraFieldID] = s
		action := "skip"
		if s.CanMap {
			action = "create"
		}
		mappings = append(mappings, CustomFieldMapping{
			JiraID:        s.JiraFieldID,
			JiraName:      s.JiraFieldName,
			JiraType:      s.JiraFieldType,
			WindshiftType: string(s.WindshiftFieldType),
			CanMap:        s.CanMap,
			Action:        action,
			PreserveRaw:   s.PreserveRaw,
		})
	}
	if got := suggestionsByID["customfield_10073"]; got.WindshiftFieldType != jira.FieldTypeNumber || !got.CanMap {
		t.Fatalf("unknown app number suggestion = %#v, want native number mapping", got)
	}
	if got := suggestionsByID["customfield_10031"]; got.WindshiftFieldType != jira.FieldTypeTextarea || !got.PreserveRaw {
		t.Fatalf("unknown object suggestion = %#v, want raw textarea preservation", got)
	}
	for _, suggestion := range suggestions {
		if suggestion.JiraFieldName == "Request participants" &&
			suggestion.WindshiftFieldType != jira.FieldTypeMultiUser {
			t.Fatalf("request participants suggestion = %#v, want multi_user", suggestion)
		}
	}

	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertDummyJiraImportJob(t, db)

	h := NewJiraImportHandler(db, "jira-custom-field-fixture-test-secret-32-bytes", "", blobstore.Disabled())
	fieldMap, _, err := h.ensureCustomFields(context.Background(), "fixture-job", mappings, nil, nil)
	if err != nil {
		t.Fatalf("ensure custom fields: %v", err)
	}

	// The captured baseline contained only 46 suggestions. All 95 definitions
	// must now remain visible, with supported first-class and explicitly
	// unmapped fields excluded from custom_field_definitions.
	if len(fieldMap) <= fx.SuggestionCount {
		t.Fatalf("created/reused custom fields = %d, want more than captured suggestion count %d", len(fieldMap), fx.SuggestionCount)
	}
	assertCustomFieldType(t, db, fieldMap["customfield_10073"], "number")
	assertCustomFieldType(t, db, fieldMap["customfield_10031"], "textarea")

	for _, mapping := range mappings {
		fieldID, ok := fieldMap[mapping.JiraID]
		if mapping.JiraName == "Approvers" {
			if !ok {
				t.Fatalf("multi-user Jira field %s (%s) was not mapped", mapping.JiraName, mapping.JiraID)
			}
			assertCustomFieldType(t, db, fieldID, "multi_user")
		}
		if isJiraStoryPointsField(mapping) && ok {
			t.Fatalf("story points field %s should map to items.story_points, not custom field %d", mapping.JiraID, fieldID)
		}
		if isJiraSprintField(mapping) && ok {
			t.Fatalf("sprint field %s should map to items.iteration_id, not custom field %d", mapping.JiraID, fieldID)
		}
		if mapping.JiraType == jiraRequestTypeFieldType && ok {
			t.Fatalf("request type field %s should map to items.request_type_id, not custom field %d", mapping.JiraID, fieldID)
		}
		if ok {
			assertCustomFieldMappingRow(t, db, "fixture-job", mapping.JiraID, fieldID)
		}
	}
}

func insertDummyJiraImportJob(t *testing.T, db database.Database) {
	t.Helper()
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_connections (id, instance_url, email, encrypted_credentials, instance_name, deployment_type)
		VALUES ('fixture-connection', 'https://example.invalid', 'redacted@example.invalid', '{}', 'Fixture Jira', 'cloud')
	`); err != nil {
		t.Fatalf("insert dummy Jira connection: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_jobs (id, connection_id, status, scope, config_json)
		VALUES ('fixture-job', 'fixture-connection', 'running', 'work_items', '{}')
	`); err != nil {
		t.Fatalf("insert dummy Jira import job: %v", err)
	}
}

func assertCustomFieldType(t *testing.T, db database.Database, fieldID int, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`SELECT field_type FROM custom_field_definitions WHERE id = ?`, fieldID).Scan(&got); err != nil {
		t.Fatalf("load custom field %d: %v", fieldID, err)
	}
	if got != want {
		t.Fatalf("custom field %d type = %q, want %q", fieldID, got, want)
	}
}

func assertCustomFieldMappingRow(t *testing.T, db database.Database, jobID, jiraID string, fieldID int) {
	t.Helper()
	var mappedID int
	var metadata string
	if err := db.QueryRow(`
		SELECT windshift_id, metadata_json
		FROM jira_import_id_mappings
		WHERE job_id = ? AND entity_type = 'custom_field' AND jira_id = ?
	`, jobID, jiraID).Scan(&mappedID, &metadata); err != nil {
		t.Fatalf("load custom field mapping row for %s: %v", jiraID, err)
	}
	if mappedID != fieldID {
		t.Fatalf("custom field mapping %s windshift_id = %d, want %d", jiraID, mappedID, fieldID)
	}
	if strings.TrimSpace(metadata) == "" {
		t.Fatalf("custom field mapping %s has empty metadata", jiraID)
	}
}
