//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
)

type fakeJiraFilterClient struct {
	jira.Client
	filters *jira.FilterSearchResult
}

func (f *fakeJiraFilterClient) ListFilters(context.Context, string) (*jira.FilterSearchResult, error) {
	return f.filters, nil
}

// TestImportJiraSavedFilterWithUnsupportedPredicateIsNonExecutable is the
// WI-2068 regression: an unsupported JQL predicate must not become a
// workspace-wide executable collection. The imported collection stays
// non-executable and is flagged for review.
func TestImportJiraSavedFilterWithUnsupportedPredicateIsNonExecutable(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-unsupported-jql-secret", "", blobstore.Disabled())

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active) VALUES ('Filter Workspace', 'FLT', true) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	client := &fakeJiraFilterClient{filters: &jira.FilterSearchResult{Values: []jira.JiraFilter{{
		ID:   "1",
		Name: "My filter",
		JQL:  `project = FLT AND assignee = currentUser()`,
	}}}}
	h.importJiraSavedFilters(context.Background(), "job-jsm", "FLT", workspaceID, []string{"FLT"}, client, 0)

	var qlQuery string
	if err := db.QueryRow(`SELECT ql_query FROM collections WHERE workspace_id = ?`, workspaceID).Scan(&qlQuery); err != nil {
		t.Fatalf("load imported collection: %v", err)
	}
	if !strings.Contains(qlQuery, "1 = 0") {
		t.Fatalf("ql_query = %q, want a non-matching selector", qlQuery)
	}
	if strings.Contains(qlQuery, "assignee") {
		t.Fatalf("ql_query = %q, must not execute an untranslatable predicate", qlQuery)
	}

	var metadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'collection' AND jira_id = ?
	`, jiraFilterCollectionID("1", workspaceID)).Scan(&metadataJSON); err != nil {
		t.Fatalf("load collection mapping: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode collection mapping metadata: %v", err)
	}
	if metadata["needs_review"] != true || metadata["unsupported_membership"] != true {
		t.Fatalf("collection metadata = %#v, want needs_review/unsupported_membership", metadata)
	}
	if _, ok := metadata["unsupported_jql"].([]any); !ok {
		t.Fatalf("collection metadata = %#v, want unsupported_jql recorded", metadata)
	}
}

// TestImportJiraSavedFilterWithOnlyUnsupportedOrderingStaysExecutable proves
// unsupported ordering does not broaden or disable a complete predicate tree.
func TestImportJiraSavedFilterWithOnlyUnsupportedOrderingStaysExecutable(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-unsupported-order-secret", "", blobstore.Disabled())

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active) VALUES ('Order Workspace', 'ORD', true) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	client := &fakeJiraFilterClient{filters: &jira.FilterSearchResult{Values: []jira.JiraFilter{{
		ID:   "2",
		Name: "Ordered filter",
		JQL:  `project = ORD AND status = Open ORDER BY created DESC`,
	}}}}
	h.importJiraSavedFilters(context.Background(), "job-jsm", "ORD", workspaceID, []string{"ORD"}, client, 0)

	var qlQuery string
	if err := db.QueryRow(`SELECT ql_query FROM collections WHERE workspace_id = ?`, workspaceID).Scan(&qlQuery); err != nil {
		t.Fatalf("load imported collection: %v", err)
	}
	if strings.Contains(qlQuery, "1 = 0") {
		t.Fatalf("ql_query = %q, want the translated predicate tree, not a disabled selector", qlQuery)
	}
	if !strings.Contains(qlQuery, `status = "Open"`) {
		t.Fatalf("ql_query = %q, want the status predicate preserved", qlQuery)
	}
}
