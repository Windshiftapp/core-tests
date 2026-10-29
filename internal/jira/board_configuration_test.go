package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

// TestBoardConfigurationDecodesNumericFilterID pins the WI-1156 fix: the
// agile board-configuration API serializes the saved filter's id as a JSON
// number on some deployments, which previously aborted the entire board
// configuration decode and broke board column/status mapping import.
func TestBoardConfigurationDecodesNumericFilterID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board/17/configuration" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": 17,
			"name": "Scrumb board",
			"filter": {
				"id": 12345,
				"self": "https://jira.example.com/rest/api/2/filter/12345",
				"name": "Scrumb filter",
				"jql": "project = SCRUM"
			},
			"columnConfig": {
				"columns": [
					{"name": "To Do", "statuses": [{"id": "1", "name": "To Do"}]},
					{"name": "Done", "statuses": [{"id": "2", "name": "Done"}]}
				]
			}
		}`))
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		agileURL:   server.URL + "/rest/agile/1.0",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	config, err := jiraGetBoardConfiguration(context.Background(), client, client.agileURL, 17)
	if err != nil {
		t.Fatalf("jiraGetBoardConfiguration() error = %v", err)
	}
	if config.Filter.ID != "12345" {
		t.Errorf("filter.ID = %q, want \"12345\"", config.Filter.ID)
	}
	if len(config.ColumnConfig.Columns) != 2 || config.ColumnConfig.Columns[0].Name != "To Do" {
		t.Errorf("columns = %#v", config.ColumnConfig.Columns)
	}
}
