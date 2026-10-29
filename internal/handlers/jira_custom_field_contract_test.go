//go:build test

package handlers

import (
	"encoding/json"
	"strconv"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
	"windshift/internal/validation"
)

func TestJiraImportPersistsOnlyValidTypedCustomFieldValues(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-custom-field-contract-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	mappings := []CustomFieldMapping{
		{JiraID: "customfield_date", JiraName: "Target date", WindshiftType: "date", Action: "create"},
		{JiraID: "customfield_select", JiraName: "Color", WindshiftType: "select", Action: "create"},
		{JiraID: "customfield_multi", JiraName: "Regions", WindshiftType: "multiselect", Action: "create"},
		{JiraID: "customfield_milestone", JiraName: "Release", WindshiftType: "milestone", Action: "create"},
		{
			JiraID:        "customfield_story_points",
			JiraName:      "Story Points",
			JiraType:      "com.pyxis.greenhopper.jira:jsw-story-points",
			WindshiftType: "number",
			Action:        "skip",
		},
	}
	fieldIDs, optionIDs, err := handler.ensureCustomFields(
		t.Context(),
		"job-jsm",
		mappings,
		nil,
		map[string][]string{
			"customfield_select": {"Red"},
			"customfield_multi":  {"Alpha"},
		},
	)
	if err != nil {
		t.Fatalf("ensure importer custom fields: %v", err)
	}

	issue := jira.JiraIssue{
		ID:  "contract-1",
		Key: "APP-1",
		Fields: jira.JiraIssueFields{
			Summary:   "Typed custom field contract",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			CustomFields: map[string]interface{}{
				"customfield_date":         "2026-08-28T23:30:00.000-0700",
				"customfield_select":       map[string]interface{}{"id": "green", "value": "Green"},
				"customfield_multi":        []interface{}{map[string]interface{}{"id": "alpha", "value": "Alpha"}, map[string]interface{}{"id": "gamma", "value": "Gamma"}},
				"customfield_milestone":    map[string]interface{}{"id": "missing-version", "name": "Release 1"},
				"customfield_story_points": 8,
			},
		},
	}
	im := &jiraImportContext{
		jobID:               "job-jsm",
		progress:            &ImportProgress{},
		workspaceID:         workspaceID,
		statusMap:           map[string]int{"status": statusID},
		itemTypeMap:         map[string]int{"type": itemTypeID},
		userMap:             map[string]int{},
		usernameMap:         map[string]string{},
		portalCustomerMap:   map[string]int{},
		versionMap:          map[string]int{},
		iterationMap:        map[string]int{},
		customFieldIDMap:    fieldIDs,
		choiceOptionIDs:     optionIDs,
		customFieldMappings: mappings,
	}
	if err := handler.importIssue(t.Context(), im, &issue); err != nil {
		t.Fatalf("import issue: %v", err)
	}

	var rawValues string
	var storyPoints *float64
	if err := db.QueryRow(`
		SELECT custom_field_values, story_points
		FROM items
		WHERE workspace_id = ?
	`, workspaceID).Scan(&rawValues, &storyPoints); err != nil {
		t.Fatalf("load imported custom fields: %v", err)
	}
	var values map[string]interface{}
	if err := json.Unmarshal([]byte(rawValues), &values); err != nil {
		t.Fatalf("decode imported custom fields: %v", err)
	}
	if err := validation.ValidateAndNormalizeCustomFieldValues(db, values); err != nil {
		t.Fatalf("persisted values violate the field contract: %v", err)
	}
	if got := values[strconv.Itoa(fieldIDs["customfield_date"])]; got != "2026-08-28" {
		t.Fatalf("date value = %#v, want 2026-08-28", got)
	}
	if _, exists := values[strconv.Itoa(fieldIDs["customfield_select"])]; exists {
		t.Fatalf("unresolved select persisted as typed value: %#v", values)
	}
	if _, exists := values[strconv.Itoa(fieldIDs["customfield_milestone"])]; exists {
		t.Fatalf("unresolved milestone persisted as typed value: %#v", values)
	}
	wantAlpha := optionIDs["customfield_multi"]["alpha"]
	multi, _ := values[strconv.Itoa(fieldIDs["customfield_multi"])].([]int)
	if len(multi) != 1 || multi[0] != wantAlpha {
		t.Fatalf("multiselect value = %#v, want [%v]; all values = %#v", multi, wantAlpha, values)
	}
	for _, jiraFieldID := range []string{"customfield_select", "customfield_multi", "customfield_milestone"} {
		if _, exists := values["_jira_custom_field_"+jiraFieldID]; !exists {
			t.Fatalf("missing raw metadata for %s: %#v", jiraFieldID, values)
		}
	}
	if storyPoints != nil {
		t.Fatalf("skipped Story Points value = %v, want nil", *storyPoints)
	}
}
