package handlers

import (
	"reflect"
	"testing"

	"windshift/internal/jira"
)

func TestJiraPreservationLabels(t *testing.T) {
	issue := &jira.JiraIssue{
		Fields: jira.JiraIssueFields{
			Components: []jira.JiraComponent{
				{Name: "API"},
				{Name: ""},
				{Name: " Frontend "},
			},
			Versions: []jira.JiraVersion{
				{Name: "1.0"},
				{Name: " 2.0 "},
				{Name: ""},
			},
		},
	}

	got := jiraPreservationLabels(issue)
	want := []string{"component:API", "component:Frontend", "affects:1.0", "affects:2.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("jiraPreservationLabels() = %#v, want %#v", got, want)
	}
}

func TestAffectedVersionOptionValuesPreservesAllMappedVersions(t *testing.T) {
	issue := &jira.JiraIssue{
		Fields: jira.JiraIssueFields{
			Versions: []jira.JiraVersion{
				{ID: "missing", Name: "Missing"},
				{ID: "v2", Name: "2.0"},
				{ID: "v3", Name: "3.0"},
				{ID: "v2", Name: "2.0 duplicate"},
			},
		},
	}

	got := affectedVersionOptionValues(issue, &jiraAffectsVersionCustomField{OptionIDsByJiraID: map[string]int{"v2": 22, "v3": 33}})
	want := []int{22, 33}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("affectedVersionOptionValues() = %#v, want %#v", got, want)
	}
}

func TestExtractJiraAssetCustomFieldValue(t *testing.T) {
	issueFields := &jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_100": []interface{}{
			map[string]interface{}{"objectKey": "LAP-1", "label": "Laptop 1"},
			map[string]interface{}{"objectKey": "MON-2", "label": "Monitor 2"},
		},
	}}

	got, ok := extractCustomFieldValueWithOptions(CustomFieldMapping{
		JiraID:        "customfield_100",
		WindshiftType: "asset",
		Action:        "create",
	}, issueFields, nil, nil, nil)
	if !ok {
		t.Fatal("extractCustomFieldValue() did not return Jira asset value")
	}
	if got != "Laptop 1\nMonitor 2" {
		t.Fatalf("asset value = %#v, want newline-separated labels", got)
	}
}

func TestExtractCustomFieldValueUsesChoiceOptionIDs(t *testing.T) {
	fields := &jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_select": map[string]interface{}{"id": "900", "value": "Red"},
		"customfield_multi": []interface{}{
			map[string]interface{}{"id": "901", "value": "Beta"},
			map[string]interface{}{"id": "902", "value": "Alpha"},
			map[string]interface{}{"id": "903", "value": "beta"},
		},
	}}

	got, ok := extractCustomFieldValueWithOptions(CustomFieldMapping{
		JiraID: "customfield_select", WindshiftType: "select", Action: "create",
	}, fields, nil, nil, map[string]int{"red": 7})
	if !ok || got != 7 {
		t.Fatalf("select value = %#v, %v; want option ID 7", got, ok)
	}

	got, ok = extractCustomFieldValueWithOptions(CustomFieldMapping{
		JiraID: "customfield_multi", WindshiftType: "multiselect", Action: "create",
	}, fields, nil, nil, map[string]int{"alpha": 11, "beta": 12})
	if !ok || !reflect.DeepEqual(got, []int{12, 11}) {
		t.Fatalf("multiselect value = %#v, %v; want [12 11]", got, ok)
	}
}

func TestExtractCustomFieldValueNormalizesJiraDatetimeToDateOnly(t *testing.T) {
	fields := &jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_date": "2026-08-28T23:30:00.000-0700",
	}}

	got, ok := extractCustomFieldValueWithOptions(CustomFieldMapping{
		JiraID: "customfield_date", WindshiftType: "date", Action: "create",
	}, fields, nil, nil, nil)
	if !ok || got != "2026-08-28" {
		t.Fatalf("date value = %#v, %v; want 2026-08-28", got, ok)
	}
}

func TestExtractCustomFieldValueRejectsUnresolvedTypedValues(t *testing.T) {
	fields := &jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_select":    map[string]interface{}{"id": "900", "value": "Red"},
		"customfield_multi":     []interface{}{map[string]interface{}{"id": "901", "value": "Alpha"}},
		"customfield_milestone": map[string]interface{}{"id": "version-1", "name": "Release 1"},
		"customfield_date":      "not-a-date",
	}}
	tests := []struct {
		name       string
		mapping    CustomFieldMapping
		versionMap map[string]int
		choices    map[string]int
	}{
		{name: "select without mapped option", mapping: CustomFieldMapping{JiraID: "customfield_select", WindshiftType: "select", Action: "create"}},
		{name: "multiselect without mapped options", mapping: CustomFieldMapping{JiraID: "customfield_multi", WindshiftType: "multiselect", Action: "create"}},
		{name: "milestone without mapped version", mapping: CustomFieldMapping{JiraID: "customfield_milestone", WindshiftType: "milestone", Action: "create"}},
		{name: "invalid date", mapping: CustomFieldMapping{JiraID: "customfield_date", WindshiftType: "date", Action: "create"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := extractCustomFieldValueWithOptions(test.mapping, fields, nil, test.versionMap, test.choices)
			if ok {
				t.Fatalf("unresolved typed value = %#v, want omitted", got)
			}
		})
	}
}

func TestUnresolvedJiraCustomFieldRawValuePreservesMissingAndPartialChoices(t *testing.T) {
	fields := &jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_select": map[string]interface{}{"id": "900", "value": "Red"},
		"customfield_multi": []interface{}{
			map[string]interface{}{"id": "901", "value": "Alpha"},
			map[string]interface{}{"id": "902", "value": "Beta"},
		},
	}}

	selectMapping := CustomFieldMapping{JiraID: "customfield_select", WindshiftType: "select", Action: "create"}
	selectRaw, preserve := unresolvedJiraCustomFieldRawValue(selectMapping, fields, false, nil)
	if !preserve || !reflect.DeepEqual(selectRaw, fields.CustomFields[selectMapping.JiraID]) {
		t.Fatalf("select raw fallback = %#v, %v; want complete Jira value", selectRaw, preserve)
	}

	multiMapping := CustomFieldMapping{JiraID: "customfield_multi", WindshiftType: "multiselect", Action: "create"}
	multiRaw, preserve := unresolvedJiraCustomFieldRawValue(
		multiMapping,
		fields,
		true,
		map[string]int{"alpha": 11},
	)
	if !preserve || !reflect.DeepEqual(multiRaw, fields.CustomFields[multiMapping.JiraID]) {
		t.Fatalf("multiselect raw fallback = %#v, %v; want complete Jira value", multiRaw, preserve)
	}
}

func TestUnresolvedJiraCustomFieldRawValueHonorsSkip(t *testing.T) {
	fields := &jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_select": map[string]interface{}{"id": "900", "value": "Red"},
	}}
	mapping := CustomFieldMapping{JiraID: "customfield_select", WindshiftType: "select", Action: "skip"}

	if raw, preserve := unresolvedJiraCustomFieldRawValue(mapping, fields, false, nil); preserve {
		t.Fatalf("skipped field preserved raw value %#v", raw)
	}
}

func TestSkippedStoryPointsMappingDoesNotSetStoryPoints(t *testing.T) {
	mapping := CustomFieldMapping{
		JiraID:        "customfield_story_points",
		JiraName:      "Story Points",
		JiraType:      "com.pyxis.greenhopper.jira:jsw-story-points",
		WindshiftType: "number",
		Action:        "skip",
	}
	if !isJiraStoryPointsField(mapping) {
		t.Fatal("test mapping is not recognized as Story Points")
	}
	if shouldImportJiraStoryPoints(mapping) {
		t.Fatal("skipped Story Points mapping should not be imported")
	}
}

func TestExtractCustomFieldValuePreservesOpaquePayloadAsJSON(t *testing.T) {
	fields := &jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_opaque": map[string]interface{}{
			"id": "opaque-1",
			"nested": map[string]interface{}{
				"enabled": true,
			},
		},
	}}

	got, ok := extractCustomFieldValueWithOptions(CustomFieldMapping{
		JiraID:        "customfield_opaque",
		WindshiftType: "textarea",
		Action:        "create",
		PreserveRaw:   true,
	}, fields, nil, nil, nil)
	if !ok || got != `{"id":"opaque-1","nested":{"enabled":true}}` {
		t.Fatalf("opaque value = %#v, %v", got, ok)
	}
}

func TestCollectUsersFromADFQueuesMentionOnlyUsers(t *testing.T) {
	adf := map[string]interface{}{
		"type": "doc",
		"content": []interface{}{
			map[string]interface{}{
				"type": "paragraph",
				"content": []interface{}{
					map[string]interface{}{
						"type": "mention",
						"attrs": map[string]interface{}{
							"id":   "acct-1",
							"text": "@Ada Lovelace",
						},
					},
					map[string]interface{}{
						"type": "mention",
						"attrs": map[string]interface{}{
							"id":   "already-mapped",
							"text": "@Existing User",
						},
					},
					map[string]interface{}{
						"type": "mention",
						"attrs": map[string]interface{}{
							"id":   "acct-1",
							"text": "@Ada Lovelace Duplicate",
						},
					},
				},
			},
		},
	}

	var users []JiraUserSummary
	collectUsersFromADF(adf, map[string]int{"already-mapped": 42}, &users, map[string]bool{})

	want := []JiraUserSummary{{AccountID: "acct-1", DisplayName: "Ada Lovelace"}}
	if !reflect.DeepEqual(users, want) {
		t.Fatalf("collectUsersFromADF() = %#v, want %#v", users, want)
	}
}

func TestImportedStoryPoints(t *testing.T) {
	importable := CustomFieldMapping{
		JiraID:        "customfield_story_points",
		JiraName:      "Story Points",
		JiraType:      "com.pyxis.greenhopper.jira:jsw-story-points",
		WindshiftType: "number",
		Action:        "create",
	}
	byName := CustomFieldMapping{JiraID: "customfield_other", JiraName: "Story Points", Action: "create"}
	skipped := CustomFieldMapping{JiraID: "customfield_skipped", JiraName: "Story Points", Action: "skip"}

	tests := []struct {
		name     string
		mappings []CustomFieldMapping
		fields   jira.JiraIssueFields
		want     *float64
	}{
		{
			name:     "imports a non-negative value",
			mappings: []CustomFieldMapping{importable},
			fields:   jira.JiraIssueFields{CustomFields: map[string]interface{}{"customfield_story_points": float64(8)}},
			want:     ptrFloat64(8),
		},
		{
			name:     "imports zero",
			mappings: []CustomFieldMapping{importable},
			fields:   jira.JiraIssueFields{CustomFields: map[string]interface{}{"customfield_story_points": float64(0)}},
			want:     ptrFloat64(0),
		},
		{
			name:     "skips negative values instead of persisting them",
			mappings: []CustomFieldMapping{importable},
			fields:   jira.JiraIssueFields{CustomFields: map[string]interface{}{"customfield_story_points": float64(-5)}},
			want:     nil,
		},
		{
			name:     "skips non-numeric values",
			mappings: []CustomFieldMapping{importable},
			fields:   jira.JiraIssueFields{CustomFields: map[string]interface{}{"customfield_story_points": "not a number"}},
			want:     nil,
		},
		{
			name:     "ignores skipped mappings",
			mappings: []CustomFieldMapping{skipped},
			fields:   jira.JiraIssueFields{CustomFields: map[string]interface{}{"customfield_skipped": float64(3)}},
			want:     nil,
		},
		{
			name:     "later mapping wins",
			mappings: []CustomFieldMapping{importable, byName},
			fields:   jira.JiraIssueFields{CustomFields: map[string]interface{}{"customfield_story_points": float64(2), "customfield_other": float64(5)}},
			want:     ptrFloat64(5),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := tt.fields
			got := importedStoryPoints(tt.mappings, &fields)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("importedStoryPoints() = %v, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("importedStoryPoints() = %v, want %v", got, *tt.want)
			}
		})
	}
}

func ptrFloat64(v float64) *float64 { return &v }

func TestUnresolvedJiraCustomFieldRawValuePreservesDatetimePrecision(t *testing.T) {
	fields := &jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_dt":   "2026-08-28T23:30:00.000-0700",
		"customfield_date": "2026-08-28",
	}}
	datetimeMapping := CustomFieldMapping{
		JiraID:        "customfield_dt",
		JiraType:      "com.atlassian.jira.plugin.system.customfieldtypes:datetime",
		WindshiftType: "date",
		Action:        "create",
	}

	raw, preserve := unresolvedJiraCustomFieldRawValue(datetimeMapping, fields, true, nil)
	if !preserve || raw != "2026-08-28T23:30:00.000-0700" {
		t.Fatalf("datetime raw = %#v, %v; want the original timestamp preserved", raw, preserve)
	}

	dateOnlyMapping := CustomFieldMapping{
		JiraID:        "customfield_date",
		JiraType:      "com.atlassian.jira.plugin.system.customfieldtypes:date",
		WindshiftType: "date",
		Action:        "create",
	}
	if raw, preserve := unresolvedJiraCustomFieldRawValue(dateOnlyMapping, fields, true, nil); preserve {
		t.Fatalf("date-only mapping preserved redundant raw %#v", raw)
	}

	explicitRaw := datetimeMapping
	explicitRaw.PreserveRaw = true
	if raw, preserve := unresolvedJiraCustomFieldRawValue(explicitRaw, fields, true, nil); preserve {
		t.Fatalf("PreserveRaw mapping double-preserved %#v", raw)
	}
}

func TestApplyJiraCustomFieldMappingsRetainsDatetimeSourcePrecision(t *testing.T) {
	issue := &jira.JiraIssue{Fields: jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_dt": "2026-08-28T23:30:00.000-0700",
	}}}
	im := &jiraImportContext{
		customFieldMappings: []CustomFieldMapping{{
			JiraID:        "customfield_dt",
			JiraName:      "Deployment time",
			JiraType:      "com.atlassian.jira.plugin.system.customfieldtypes:datetime",
			WindshiftType: "date",
			Action:        "create",
		}},
		customFieldIDMap: map[string]int{"customfield_dt": 42},
		choiceOptionIDs:  map[string]map[string]int{},
		userMap:          map[string]int{},
		versionMap:       map[string]int{},
	}
	values := map[string]any{}
	handler := &JiraImportHandler{}

	handler.applyJiraCustomFieldMappings(im, issue, values)

	if got := values["42"]; got != "2026-08-28" {
		t.Fatalf("date field value = %#v, want 2026-08-28", got)
	}
	preserved, ok := values["_jira_custom_field_customfield_dt"].(map[string]any)
	if !ok {
		t.Fatalf("datetime source precision not retained: %#v", values)
	}
	if preserved["value"] != "2026-08-28T23:30:00.000-0700" {
		t.Fatalf("preserved datetime = %#v, want the original timestamp", preserved["value"])
	}
}
