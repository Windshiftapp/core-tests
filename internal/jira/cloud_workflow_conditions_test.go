package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

// TestCloudWorkflowConfigurationDecodesObjectConditions is the WI-2079
// regression: Jira Cloud returns transition `conditions` as an object for some
// workflows, and decoding it must not fail the whole configured workflow read.
func TestCloudWorkflowConfigurationDecodesObjectConditions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		conditions         any
		wantConditionCount int
	}{
		{
			name: "object shape",
			conditions: map[string]any{
				"operation":       "ALL",
				"conditionGroups": []any{},
				"conditions":      []any{map[string]any{"ruleKey": "system:block-in-progress-approval"}},
			},
			wantConditionCount: 1,
		},
		{
			name:               "legacy array shape",
			conditions:         []any{map[string]any{"ruleKey": "system:block-in-progress-approval"}},
			wantConditionCount: 1,
		},
		{
			name:               "absent conditions",
			conditions:         nil,
			wantConditionCount: 0,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/workflows" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				transition := map[string]any{
					"id": "81", "name": "Draft", "type": "DIRECTED",
					"toStatusReference": "10025",
					"links":             []any{map[string]any{"fromStatusReference": "10024"}},
					"validators":        []any{},
					"actions":           []any{},
					"triggers":          []any{},
				}
				if test.conditions != nil {
					transition["conditions"] = test.conditions
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"statuses": []any{
						map[string]any{"id": "1", "statusReference": "10024"},
						map[string]any{"id": "2", "statusReference": "10025"},
					},
					"workflows": []any{map[string]any{
						"id": "wf-1", "name": "SP workflow",
						"statuses":    []any{map[string]any{"statusReference": "10024"}, map[string]any{"statusReference": "10025"}},
						"transitions": []any{transition},
					}},
				})
			}))
			t.Cleanup(server.Close)

			client := &cloudClient{
				baseURL:    server.URL + "/rest/api/3",
				authHeader: "Basic test-token",
				httpClient: server.Client(),
				limiter:    rate.NewLimiter(rate.Inf, 1),
			}

			config, err := client.GetProjectWorkflowConfiguration(context.Background(), "10002", []string{"10013"})
			if err != nil {
				t.Fatalf("GetProjectWorkflowConfiguration() error = %v", err)
			}
			if config.IssueTypeWorkflowIDs["10013"] != "wf-1" {
				t.Fatalf("issue type workflow = %q, want wf-1", config.IssueTypeWorkflowIDs["10013"])
			}
			if len(config.Workflows) != 1 || len(config.Workflows[0].Transitions) != 1 {
				t.Fatalf("workflows = %#v, want one workflow with one transition", config.Workflows)
			}
			workflow := config.Workflows[0]
			if got := workflow.Transitions[0].ConditionCount; got != test.wantConditionCount {
				t.Fatalf("ConditionCount = %d, want %d", got, test.wantConditionCount)
			}
			if workflow.Transitions[0].FromStatusIDs[0] != "1" {
				t.Fatalf("FromStatusIDs = %#v, want the resolved source status", workflow.Transitions[0].FromStatusIDs)
			}
		})
	}
}
