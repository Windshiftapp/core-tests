package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestCloudClientGetProjectWorkflowConfigurationMapsGraphAndRules(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/rest/api/3/workflows" {
			t.Fatalf("path = %s, want /rest/api/3/workflows", r.URL.Path)
		}
		var request cloudWorkflowReadRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(request.ProjectAndIssueTypes) != 1 {
			t.Fatalf("project/issue-type pairs = %#v, want exactly one", request.ProjectAndIssueTypes)
		}
		if request.ProjectAndIssueTypes[0].ProjectID != "10000" {
			t.Fatalf("project ID = %q, want 10000", request.ProjectAndIssueTypes[0].ProjectID)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"statuses": [
				{"id": "1", "statusReference": "open-ref"},
				{"id": "2", "statusReference": "done-ref"}
			],
			"workflows": [{
				"id": "wf-7",
				"name": "Delivery",
				"description": "Source workflow",
				"statuses": [
					{"statusReference": "open-ref"},
					{"statusReference": "done-ref"}
				],
				"transitions": [
					{
						"id": "10",
						"name": "Create",
						"type": "INITIAL",
						"toStatusReference": "open-ref",
						"links": [{"fromStatusReference": ""}],
						"validators": [],
						"actions": [],
						"triggers": []
					},
					{
						"id": "20",
						"name": "Finish",
						"type": "DIRECTED",
						"toStatusReference": "done-ref",
						"links": [{"fromStatusReference": "open-ref"}],
						"validators": [{"type": "PermissionValidator"}],
						"actions": [{"type": "FireIssueEvent"}],
						"triggers": [{"type": "Webhook"}],
						"conditions": [{"type": "UserIsInGroup"}]
					}
				]
			}]
		}`))
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
	config, err := client.GetProjectWorkflowConfiguration(
		context.Background(),
		"10000",
		[]string{"10002", "10001"},
	)
	if err != nil {
		t.Fatalf("GetProjectWorkflowConfiguration() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("request count = %d, want 2", requests)
	}
	if config.IssueTypeWorkflowIDs["10001"] != "wf-7" ||
		config.IssueTypeWorkflowIDs["10002"] != "wf-7" {
		t.Fatalf("issue type mapping = %#v", config.IssueTypeWorkflowIDs)
	}
	if len(config.Workflows) != 1 {
		t.Fatalf("workflow count = %d, want 1", len(config.Workflows))
	}
	workflow := config.Workflows[0]
	if len(workflow.StatusIDs) != 2 || workflow.StatusIDs[0] != "1" || workflow.StatusIDs[1] != "2" {
		t.Fatalf("status IDs = %#v, want [1 2]", workflow.StatusIDs)
	}
	if len(workflow.Transitions) != 2 {
		t.Fatalf("transition count = %d, want 2", len(workflow.Transitions))
	}
	initial := workflow.Transitions[0]
	if initial.Type != JiraWorkflowTransitionInitial || len(initial.FromStatusIDs) != 0 ||
		initial.ToStatusID != "1" {
		t.Fatalf("initial transition = %#v, want source-less transition to status 1", initial)
	}
	guarded := workflow.Transitions[1]
	if len(guarded.FromStatusIDs) != 1 || guarded.FromStatusIDs[0] != "1" || guarded.ToStatusID != "2" {
		t.Fatalf("guarded transition endpoints = %#v", guarded)
	}
	if guarded.ValidatorCount != 1 || guarded.ConditionCount != 1 ||
		guarded.ActionCount != 1 || guarded.TriggerCount != 1 {
		t.Fatalf("guarded transition rule counts = %#v", guarded)
	}
}
