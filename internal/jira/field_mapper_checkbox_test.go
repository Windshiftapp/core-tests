//go:build test

package jira

import "testing"

// WI-891: Jira single-boolean fields map to the canonical asset boolean type;
// multicheckboxes stay multiselect.
func TestMapJiraBooleanToAssetBoolean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cf   JiraCustomField
		want WindshiftFieldType
	}{
		{
			name: "schema boolean",
			cf: JiraCustomField{
				ID:     "customfield_10021",
				Name:   "Approved",
				Schema: &JiraFieldSchema{Type: "boolean"},
			},
			want: FieldTypeBoolean,
		},
		{
			name: "plugin boolean",
			cf: JiraCustomField{
				ID:   "customfield_10022",
				Name: "Approved",
				Schema: &JiraFieldSchema{
					Custom: "com.atlassian.jira.plugin.system.customfieldtypes:boolean",
				},
			},
			want: FieldTypeBoolean,
		},
		{
			name: "multicheckboxes stays multiselect",
			cf: JiraCustomField{
				ID:   "customfield_10023",
				Name: "Checks",
				Schema: &JiraFieldSchema{
					Custom: "com.atlassian.jira.plugin.system.customfieldtypes:multicheckboxes",
				},
			},
			want: FieldTypeMultiselect,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapJiraFieldToWindshift(tt.cf)
			if !got.CanMap {
				t.Fatalf("expected field to map, got %#v", got)
			}
			if got.WindshiftFieldType != tt.want {
				t.Fatalf("WindshiftFieldType = %q, want %q", got.WindshiftFieldType, tt.want)
			}
		})
	}
}
