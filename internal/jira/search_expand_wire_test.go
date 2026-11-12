package jira

import (
	"encoding/json"
	"testing"
)

// TestJQLSearchRequestSerializesExpandAsString is the WI-2063 regression: the
// enhanced-search request bean expects expand as a comma-delimited string, not
// an array. Bulk fetch keeps its array shape.
func TestJQLSearchRequestSerializesExpandAsString(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(JQLSearchRequest{
		JQL:        `project = APP`,
		MaxResults: 100,
		Expand:     []string{"changelog", "renderedFields"},
	})
	if err != nil {
		t.Fatalf("marshal JQLSearchRequest: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode JQLSearchRequest: %v", err)
	}
	if got, want := body["expand"], "changelog,renderedFields"; got != want {
		t.Fatalf("enhanced-search expand = %#v, want %q", got, want)
	}

	rawBulk, err := json.Marshal(BulkFetchRequest{
		IssueIdsOrKeys: []string{"APP-1"},
		Expand:         []string{"changelog"},
	})
	if err != nil {
		t.Fatalf("marshal BulkFetchRequest: %v", err)
	}
	var bulkBody map[string]any
	if err := json.Unmarshal(rawBulk, &bulkBody); err != nil {
		t.Fatalf("decode BulkFetchRequest: %v", err)
	}
	expand, ok := bulkBody["expand"].([]any)
	if !ok || len(expand) != 1 || expand[0] != "changelog" {
		t.Fatalf("bulk-fetch expand = %#v, want [changelog]", bulkBody["expand"])
	}
}

// TestJQLSearchRequestOmitsEmptyExpand keeps the request body free of an empty
// expand field so Jira applies its own default.
func TestJQLSearchRequestOmitsEmptyExpand(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(JQLSearchRequest{JQL: `project = APP`})
	if err != nil {
		t.Fatalf("marshal JQLSearchRequest: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode JQLSearchRequest: %v", err)
	}
	if _, present := body["expand"]; present {
		t.Fatalf("expand = %#v, want omitted", body["expand"])
	}
}
