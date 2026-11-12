//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"windshift/internal/jira"
)

// fakeRequestTypeReconcileClient serves the field list and request search the
// reconciliation reads.
type fakeRequestTypeReconcileClient struct {
	jira.Client
	fields []jira.JiraCustomField
	issues []jira.JiraIssue
}

func (f *fakeRequestTypeReconcileClient) GetProject(context.Context, string) (*jira.JiraProject, error) {
	return &jira.JiraProject{ID: "10100", Key: "WSSD", ProjectType: "service_desk"}, nil
}

func (f *fakeRequestTypeReconcileClient) GetProjectFields(context.Context, []string) ([]jira.JiraCustomField, error) {
	return f.fields, nil
}

func (f *fakeRequestTypeReconcileClient) SearchIssuesJQL(context.Context, jira.JQLSearchRequest) (*jira.JQLSearchResponse, error) {
	return &jira.JQLSearchResponse{Issues: f.issues}, nil
}

func reconcileIssue(issueTypeID, requestTypeID string) jira.JiraIssue {
	return jira.JiraIssue{Fields: jira.JiraIssueFields{
		IssueType: &jira.JiraIssueType{ID: issueTypeID},
		CustomFields: map[string]any{
			"customfield_10201": map[string]any{
				"requestType": map[string]any{"id": requestTypeID},
			},
		},
	}}
}

// TestDeriveJiraRequestTypeIssueTypesJoinsRequestsToIssueTypes is the WI-2073
// reconciliation: Data Center omits issueTypeId from the request-type DTO, so
// the association is derived from requests the import already reads.
func TestDeriveJiraRequestTypeIssueTypesJoinsRequestsToIssueTypes(t *testing.T) {
	t.Parallel()

	client := &fakeRequestTypeReconcileClient{
		fields: []jira.JiraCustomField{{ID: "customfield_10201", Name: "Customer Request Type"}},
		issues: []jira.JiraIssue{
			reconcileIssue("10200", "1"),
			reconcileIssue("10200", "2"),
			reconcileIssue("10201", "5"),
		},
	}
	requestTypes := []jira.JiraServiceDeskRequestType{{ID: "1"}, {ID: "2"}, {ID: "5"}, {ID: "4"}}

	got := deriveJiraRequestTypeIssueTypes(context.Background(), client, "WSSD", requestTypes)
	want := map[string]string{"1": "10200", "2": "10200", "5": "10201"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("derived issue types = %#v, want %#v", got, want)
	}
}

// TestDeriveJiraRequestTypeIssueTypesSkipsCompleteCatalogs proves the scan is
// not run when the DTO already supplies every issue type (Cloud).
func TestDeriveJiraRequestTypeIssueTypesSkipsCompleteCatalogs(t *testing.T) {
	t.Parallel()

	client := &fakeRequestTypeReconcileClient{
		fields: []jira.JiraCustomField{{ID: "customfield_10201", Name: "Customer Request Type"}},
		issues: []jira.JiraIssue{reconcileIssue("10200", "1")},
	}
	requestTypes := []jira.JiraServiceDeskRequestType{{ID: "1", IssueTypeID: "10200"}}

	if got := deriveJiraRequestTypeIssueTypes(context.Background(), client, "WSSD", requestTypes); got != nil {
		t.Fatalf("derived issue types = %#v, want nil when the DTO is complete", got)
	}
}

// TestDeriveJiraRequestTypeIssueTypesWithoutRequestTypeField proves the
// reconciliation degrades to "no evidence" instead of guessing.
func TestDeriveJiraRequestTypeIssueTypesWithoutRequestTypeField(t *testing.T) {
	t.Parallel()

	client := &fakeRequestTypeReconcileClient{
		fields: []jira.JiraCustomField{{ID: "customfield_10001", Name: "Story Points"}},
		issues: []jira.JiraIssue{reconcileIssue("10200", "1")},
	}

	if got := deriveJiraRequestTypeIssueTypes(context.Background(), client, "WSSD", []jira.JiraServiceDeskRequestType{{ID: "1"}}); got != nil {
		t.Fatalf("derived issue types = %#v, want nil without the request-type field", got)
	}
}

// TestEnsureJiraRequestTypesUsesDerivedIssueType proves a reconciled issue type
// produces a native request type instead of being skipped.
func TestEnsureJiraRequestTypesUsesDerivedIssueType(t *testing.T) {
	db, h, workspaceID, itemTypeID := requestTypeFixture(t)
	channelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10100", Key: "WSSD", Name: "Service Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10100", ProjectKey: "WSSD"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("ensure Jira portal: %v", err)
	}

	requestTypeMap, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID,
		[]jira.JiraServiceDeskRequestType{
			{ID: "1", Name: "IT help", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
		},
		map[string]int{"10200": itemTypeID},
		map[string]string{"1": "10200"},
	)
	if err != nil {
		t.Fatalf("ensure Jira request types: %v", err)
	}
	if requestTypeMap["1"] == 0 {
		t.Fatal("derived request type was not created")
	}
	active, metadata := importedRequestTypeState(t, db, requestTypeMap["1"])
	if !active {
		t.Fatal("reconciled request type should be active")
	}
	if metadata["jira_issue_type_id"] != "10200" {
		t.Fatalf("mapping metadata = %#v, want jira_issue_type_id 10200", metadata)
	}
}

// TestEnsureJiraRequestTypesReportsUnmappedTypes proves a request type with no
// observable issue type is reported instead of silently dropped.
func TestEnsureJiraRequestTypesReportsUnmappedTypes(t *testing.T) {
	db, h, workspaceID, itemTypeID := requestTypeFixture(t)
	channelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10100", Key: "WSSD", Name: "Service Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10100", ProjectKey: "WSSD"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("ensure Jira portal: %v", err)
	}

	requestTypeMap, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID,
		[]jira.JiraServiceDeskRequestType{
			{ID: "1", Name: "IT help", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
			{ID: "4", Name: "Employee exit", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
		},
		map[string]int{"10200": itemTypeID},
		map[string]string{"1": "10200"},
	)
	if err != nil {
		t.Fatalf("ensure Jira request types: %v", err)
	}
	if requestTypeMap["1"] == 0 {
		t.Fatal("mapped request type was not created")
	}
	if _, ok := requestTypeMap["4"]; ok {
		t.Fatal("unmapped request type must not be created")
	}

	var metadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'fidelity_finding' AND jira_id = 'request-type:4'
	`).Scan(&metadataJSON); err != nil {
		t.Fatalf("load unmapped request type finding: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode finding metadata: %v", err)
	}
	if metadata["code"] != "jira_request_type_unmapped" || metadata["disposition"] != "unavailable" {
		t.Fatalf("finding metadata = %#v, want jira_request_type_unmapped/unavailable", metadata)
	}
	if metadata["request_type_name"] != "Employee exit" {
		t.Fatalf("finding metadata = %#v, want the source request type name", metadata)
	}
}
