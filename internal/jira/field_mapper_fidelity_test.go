package jira

import (
	"strings"
	"testing"
)

func TestMapJiraFieldToWindshiftDatetimeNoteMentionsMetadataRetention(t *testing.T) {
	t.Parallel()

	suggestion := MapJiraFieldToWindshift(JiraCustomField{
		ID:   "customfield_77",
		Name: "Deployment time",
		Schema: &JiraFieldSchema{
			Type:   "datetime",
			Custom: "com.atlassian.jira.plugin.system.customfieldtypes:datetime",
		},
	})
	if !strings.Contains(suggestion.Notes, "retained in the item's Jira custom-field metadata") {
		t.Fatalf("notes = %q, want the source precision retained in metadata", suggestion.Notes)
	}
}

func TestSuggestFieldMappingsKeepsUnknownSchemaBackedFieldsVisible(t *testing.T) {
	fields := []JiraCustomField{
		{
			ID:   "customfield_1",
			Name: "App score",
			Schema: &JiraFieldSchema{
				Type:   "number",
				Custom: "vendor.app:score",
			},
		},
		{
			ID:   "customfield_2",
			Name: "App payload",
			Schema: &JiraFieldSchema{
				Type:   "object",
				Custom: "vendor.app:payload",
			},
		},
		{
			ID:   "customfield_3",
			Name: "Request participants",
			Schema: &JiraFieldSchema{
				Type:   "array",
				Items:  "user",
				Custom: "com.atlassian.servicedesk:sd-request-participants",
			},
		},
		{
			ID:   "customfield_4",
			Name: "App personas",
			Schema: &JiraFieldSchema{
				Type:   "array",
				Items:  "option2",
				Custom: "vendor.app:personas",
			},
		},
	}

	got := SuggestFieldMappings(fields)
	if len(got) != len(fields) {
		t.Fatalf("suggestions = %d, want %d", len(got), len(fields))
	}
	if got[0].WindshiftFieldType != FieldTypeNumber || !got[0].CanMap || got[0].PreserveRaw {
		t.Fatalf("number suggestion = %#v", got[0])
	}
	if got[1].WindshiftFieldType != FieldTypeTextarea || !got[1].CanMap || !got[1].PreserveRaw {
		t.Fatalf("object suggestion = %#v", got[1])
	}
	if got[2].WindshiftFieldType != FieldTypeMultiUser || !got[2].CanMap {
		t.Fatalf("request participants suggestion = %#v", got[2])
	}
	if got[3].WindshiftFieldType != FieldTypeMultiselect || !got[3].CanMap || got[3].PreserveRaw {
		t.Fatalf("option2 array suggestion = %#v", got[3])
	}
}
