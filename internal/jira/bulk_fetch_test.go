package jira

import (
	"encoding/json"
	"testing"
)

// TestBulkFetchResponseDecodesIssueErrors pins the WI-1701 wire contract:
// Jira Cloud answers POST /rest/api/3/issue/bulkfetch with HTTP 200, resolved
// issues under "issues", and per-issue failures under "issueErrors".
func TestBulkFetchResponseDecodesIssueErrors(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"issues": [
			{"id": "10001", "key": "APP-1", "fields": {"summary": "Visible issue"}}
		],
		"issueErrors": [
			{"issueIdOrKey": "APP-2", "errorMessages": ["Issue does not exist or you do not have permission to see it."]},
			{"issueIdOrKey": "APP-3", "errors": {"summary": "Field is not available"}}
		]
	}`)

	var result BulkFetchResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode bulk fetch response: %v", err)
	}
	if len(result.Issues) != 1 || result.Issues[0].Key != "APP-1" {
		t.Fatalf("issues = %#v, want one APP-1", result.Issues)
	}
	if len(result.IssueErrors) != 2 {
		t.Fatalf("issueErrors = %#v, want 2 entries", result.IssueErrors)
	}
	if result.IssueErrors[0].IssueIDOrKey != "APP-2" {
		t.Fatalf("first issue error key = %q, want APP-2", result.IssueErrors[0].IssueIDOrKey)
	}
	if got, want := result.IssueErrors[0].Message(), "Issue does not exist or you do not have permission to see it."; got != want {
		t.Fatalf("first issue error message = %q, want %q", got, want)
	}
	if got, want := result.IssueErrors[1].Message(), "summary: Field is not available"; got != want {
		t.Fatalf("field error message = %q, want %q", got, want)
	}
}
