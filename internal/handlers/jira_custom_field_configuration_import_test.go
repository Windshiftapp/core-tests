package handlers

import (
	"encoding/json"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
	"windshift/internal/testutils"
)

func TestConfiguredJiraChoiceOptionsAndContextsArePreserved(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	insertJiraImportConnection(t, tdb.Database, "connection-field-config")
	insertJiraImportJob(t, tdb.Database, "job-field-config", "connection-field-config", "running", []string{"APP"})

	mapping := CustomFieldMapping{
		JiraID:        "customfield_100",
		JiraName:      "Environment",
		JiraType:      "com.atlassian.jira.plugin.system.customfieldtypes:cascadingselect",
		WindshiftType: "select",
		Action:        "create",
	}
	captures := map[string]jiraCustomFieldConfigurationCapture{
		mapping.JiraID: {
			configuration: &jira.JiraCustomFieldConfiguration{
				FieldID: mapping.JiraID,
				Contexts: []jira.JiraCustomFieldContext{{
					ID:           "20",
					Name:         "Application bugs",
					ProjectIDs:   []string{"200"},
					IssueTypeIDs: []string{"bug"},
					Options: []jira.JiraCustomFieldContextOption{
						{ID: "1", Value: "Production"},
						{ID: "2", Value: "EU", ParentOptionID: "1", Disabled: true},
					},
					Defaults: []jira.JiraCustomFieldDefaultValue{{
						IssueTypeID: "bug",
						Value:       map[string]interface{}{"optionId": "2"},
					}},
				}},
			},
		},
	}
	labels := map[string][]string{mapping.JiraID: {"Observed"}}
	mergeJiraConfiguredChoiceLabels(labels, captures)
	if got := labels[mapping.JiraID]; len(got) != 3 ||
		got[0] != "Observed" || got[1] != "Production" || got[2] != "Production / EU" {
		t.Fatalf("merged labels = %#v", got)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-field-config-test-secret-32", "", blobstore.Disabled())
	fieldIDs, optionIDs, err := handler.ensureCustomFields(
		t.Context(),
		"job-field-config",
		[]CustomFieldMapping{mapping},
		nil,
		labels,
		captures,
	)
	if err != nil {
		t.Fatalf("ensure custom fields: %v", err)
	}
	if fieldIDs[mapping.JiraID] == 0 ||
		optionIDs[mapping.JiraID]["production / eu"] == 0 {
		t.Fatalf("field/options = %#v / %#v", fieldIDs, optionIDs)
	}

	var metadataJSON string
	if err := tdb.QueryRow(`
		SELECT metadata_json
		FROM jira_import_id_mappings
		WHERE job_id = ? AND entity_type = 'custom_field' AND jira_id = ?
	`, "job-field-config", mapping.JiraID).Scan(&metadataJSON); err != nil {
		t.Fatalf("load field mapping metadata: %v", err)
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata["configuration_status"] != "preserved" ||
		metadata["configured_option_count"] != float64(2) ||
		metadata["disabled_option_count"] != float64(1) ||
		metadata["default_count"] != float64(1) {
		t.Fatalf("configuration metadata = %#v", metadata)
	}
	if contexts, ok := metadata["contexts"].([]interface{}); !ok || len(contexts) != 1 {
		t.Fatalf("preserved contexts = %#v", metadata["contexts"])
	}
}
