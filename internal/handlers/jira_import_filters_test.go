//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
)

// fakeJiraBoardFilterClient serves saved filters, boards, and board
// configurations so the filter/board import paths can be exercised without a
// live Jira instance.
type fakeJiraBoardFilterClient struct {
	jira.Client
	filters     *jira.FilterSearchResult
	boards      []jira.JiraBoard
	configs     map[int]*jira.JiraBoardConfiguration
	filtersByID map[string]*jira.JiraFilter
}

func (f *fakeJiraBoardFilterClient) ListFilters(context.Context, string) (*jira.FilterSearchResult, error) {
	return f.filters, nil
}

func (f *fakeJiraBoardFilterClient) ListBoards(context.Context, string) (*jira.BoardListResult, error) {
	return &jira.BoardListResult{Values: f.boards}, nil
}

func (f *fakeJiraBoardFilterClient) GetBoardConfiguration(_ context.Context, boardID int) (*jira.JiraBoardConfiguration, error) {
	return f.configs[boardID], nil
}

func (f *fakeJiraBoardFilterClient) GetFilter(_ context.Context, filterID string) (*jira.JiraFilter, error) {
	return f.filtersByID[filterID], nil
}

// failingJiraFilterClient simulates a permission-denied saved-filter listing.
type failingJiraFilterClient struct {
	jira.Client
}

func (f *failingJiraFilterClient) ListFilters(context.Context, string) (*jira.FilterSearchResult, error) {
	return nil, errors.New("403 forbidden")
}

func newJiraFilterScopeHandler(t *testing.T) (*JiraImportHandler, database.Database) {
	t.Helper()
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-filter-scope-secret", "", blobstore.Disabled())
	return h, db
}

func createFilterScopeWorkspace(t *testing.T, db database.Database, name, key string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES (?, ?, true) RETURNING id`, name, key).Scan(&id); err != nil {
		t.Fatalf("create workspace %s: %v", key, err)
	}
	return id
}

func countWorkspaceCollections(t *testing.T, db database.Database, workspaceID int) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collections WHERE workspace_id = ?`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count collections: %v", err)
	}
	return count
}

func workspaceCollectionQL(t *testing.T, db database.Database, workspaceID int) string {
	t.Helper()
	var ql string
	if err := db.QueryRow(`SELECT ql_query FROM collections WHERE workspace_id = ?`, workspaceID).Scan(&ql); err != nil {
		t.Fatalf("load collection ql: %v", err)
	}
	return ql
}

func mappingExists(t *testing.T, db database.Database, jobID, entityType, jiraID string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM jira_import_id_mappings WHERE job_id = ? AND entity_type = ? AND jira_id = ?`,
		jobID, entityType, jiraID,
	).Scan(&count); err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	return count > 0
}

func filterClientWith(filters ...jira.JiraFilter) *fakeJiraBoardFilterClient {
	byID := make(map[string]*jira.JiraFilter, len(filters))
	for i := range filters {
		byID[filters[i].ID] = &filters[i]
	}
	return &fakeJiraBoardFilterClient{
		filters:     &jira.FilterSearchResult{Values: filters, Partial: true, PartialReason: "Data Center exposes favorites only"},
		filtersByID: byID,
	}
}

func boardConfigurationWithFilter(t *testing.T, boardID int, filterID string) *jira.JiraBoardConfiguration {
	t.Helper()
	raw := fmt.Sprintf(`{"id":%d,"name":"Board %d","type":"scrum","filter":{"id":%s}}`, boardID, boardID, filterID)
	var config jira.JiraBoardConfiguration
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("decode board configuration: %v", err)
	}
	return &config
}

// TestImportJiraSavedFiltersScopesFavoriteToOwningWorkspace covers the WI-2076
// reproduction: a favorite whose JQL targets project B must not become a
// runnable collection in project A's workspace.
func TestImportJiraSavedFiltersScopesFavoriteToOwningWorkspace(t *testing.T) {
	h, db := newJiraFilterScopeHandler(t)
	workspaceA := createFilterScopeWorkspace(t, db, "Workspace A", "WSA")
	workspaceB := createFilterScopeWorkspace(t, db, "Workspace B", "WSB")

	client := filterClientWith(jira.JiraFilter{ID: "100", Name: "B open issues", JQL: `project = B AND status = Open`})

	// Import A first: the favorite targets B, so A must not get a collection.
	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspaceA, []string{"A"}, client, 0)
	if got := countWorkspaceCollections(t, db, workspaceA); got != 0 {
		t.Fatalf("collections in workspace A = %d, want 0", got)
	}
	if mappingExists(t, db, "job-jsm", "collection", jiraFilterCollectionID("100", workspaceA)) {
		t.Fatal("workspace A must not record a mapping for a filter scoped to project B")
	}

	// Import B: the favorite belongs here and keeps its translated predicate.
	h.importJiraSavedFilters(context.Background(), "job-jsm", "B", workspaceB, []string{"B"}, client, 0)
	if got := countWorkspaceCollections(t, db, workspaceB); got != 1 {
		t.Fatalf("collections in workspace B = %d, want 1", got)
	}
	ql := workspaceCollectionQL(t, db, workspaceB)
	if !strings.Contains(ql, fmt.Sprintf("workspace_id = %d", workspaceB)) {
		t.Fatalf("ql = %q, want workspace %d scoping", ql, workspaceB)
	}
	if !strings.Contains(ql, `status = "Open"`) {
		t.Fatalf("ql = %q, want the status predicate preserved", ql)
	}
	if !mappingExists(t, db, "job-jsm", "collection", jiraFilterCollectionID("100", workspaceB)) {
		t.Fatal("workspace B must record the filter mapping")
	}
}

// TestImportJiraSavedFiltersScopesFavoriteReverseOrder proves the outcome does
// not depend on which project is processed first.
func TestImportJiraSavedFiltersScopesFavoriteReverseOrder(t *testing.T) {
	h, db := newJiraFilterScopeHandler(t)
	workspaceA := createFilterScopeWorkspace(t, db, "Workspace A", "WSA")
	workspaceB := createFilterScopeWorkspace(t, db, "Workspace B", "WSB")

	client := filterClientWith(jira.JiraFilter{ID: "100", Name: "B open issues", JQL: `project = B AND status = Open`})

	h.importJiraSavedFilters(context.Background(), "job-jsm", "B", workspaceB, []string{"B"}, client, 0)
	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspaceA, []string{"A"}, client, 0)

	if got := countWorkspaceCollections(t, db, workspaceB); got != 1 {
		t.Fatalf("collections in workspace B = %d, want 1", got)
	}
	if got := countWorkspaceCollections(t, db, workspaceA); got != 0 {
		t.Fatalf("collections in workspace A = %d, want 0", got)
	}
}

// TestImportJiraSavedFiltersSkipsUnrelatedFavoriteInSingleProjectImport covers
// a single-project import where the only favorite belongs to another project.
func TestImportJiraSavedFiltersSkipsUnrelatedFavoriteInSingleProjectImport(t *testing.T) {
	h, db := newJiraFilterScopeHandler(t)
	workspace := createFilterScopeWorkspace(t, db, "Workspace A", "WSA")

	client := filterClientWith(jira.JiraFilter{ID: "101", Name: "Other project", JQL: `project = OTHER`})

	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspace, []string{"A"}, client, 0)

	if got := countWorkspaceCollections(t, db, workspace); got != 0 {
		t.Fatalf("collections = %d, want 0 for an unrelated favorite", got)
	}
}

// TestImportJiraSavedFiltersDedupesMergedWorkspace covers multi-project JQL:
// projects A and B share one workspace, and the filter referencing both is
// imported once with its predicate preserved.
func TestImportJiraSavedFiltersDedupesMergedWorkspace(t *testing.T) {
	h, db := newJiraFilterScopeHandler(t)
	workspace := createFilterScopeWorkspace(t, db, "Merged", "WSM")

	client := filterClientWith(jira.JiraFilter{ID: "200", Name: "A or B open", JQL: `project IN (A, B) AND status = Open`})

	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspace, []string{"A", "B"}, client, 0)
	h.importJiraSavedFilters(context.Background(), "job-jsm", "B", workspace, []string{"A", "B"}, client, 0)

	if got := countWorkspaceCollections(t, db, workspace); got != 1 {
		t.Fatalf("collections = %d, want 1 after dedupe", got)
	}
	ql := workspaceCollectionQL(t, db, workspace)
	if strings.Contains(ql, "1 = 0") {
		t.Fatalf("ql = %q, want an executable selector for a fully covered workspace", ql)
	}
	if !strings.Contains(ql, `status = "Open"`) {
		t.Fatalf("ql = %q, want the status predicate preserved", ql)
	}
}

// TestImportJiraSavedFiltersNarrowsMergedWorkspace proves a predicate that
// selects only part of a merged workspace stays non-executable and records why.
func TestImportJiraSavedFiltersNarrowsMergedWorkspace(t *testing.T) {
	h, db := newJiraFilterScopeHandler(t)
	workspace := createFilterScopeWorkspace(t, db, "Merged", "WSM")

	client := filterClientWith(jira.JiraFilter{ID: "300", Name: "Only A", JQL: `project = A AND status = Open`})

	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspace, []string{"A", "B"}, client, 0)

	if got := countWorkspaceCollections(t, db, workspace); got != 1 {
		t.Fatalf("collections = %d, want 1 kept for review", got)
	}
	ql := workspaceCollectionQL(t, db, workspace)
	if !strings.Contains(ql, "1 = 0") {
		t.Fatalf("ql = %q, want a non-matching selector for an unrepresentable predicate", ql)
	}
	findingID := fmt.Sprintf("filter-scope:300:ws%d", workspace)
	if !mappingExists(t, db, "job-jsm", "fidelity_finding", findingID) {
		t.Fatalf("expected fidelity finding %q", findingID)
	}
}

// TestImportJiraSavedFiltersReimportIsIdempotent proves reimport reuses the
// same workspace-scoped mapping instead of duplicating the collection.
func TestImportJiraSavedFiltersReimportIsIdempotent(t *testing.T) {
	h, db := newJiraFilterScopeHandler(t)
	workspace := createFilterScopeWorkspace(t, db, "Workspace A", "WSA")

	client := filterClientWith(jira.JiraFilter{ID: "400", Name: "A open", JQL: `project = A AND status = Open`})

	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspace, []string{"A"}, client, 0)
	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspace, []string{"A"}, client, 0)

	if got := countWorkspaceCollections(t, db, workspace); got != 1 {
		t.Fatalf("collections = %d, want 1 after reimport", got)
	}
}

// TestImportJiraSavedFiltersRecordsUnavailableDiscovery proves a permission or
// transport failure is distinguishable from "the project has no saved filters".
func TestImportJiraSavedFiltersRecordsUnavailableDiscovery(t *testing.T) {
	h, db := newJiraFilterScopeHandler(t)
	workspace := createFilterScopeWorkspace(t, db, "Workspace A", "WSA")

	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspace, []string{"A"}, &failingJiraFilterClient{}, 0)

	if got := countWorkspaceCollections(t, db, workspace); got != 0 {
		t.Fatalf("collections = %d, want 0 when discovery fails", got)
	}
	if !mappingExists(t, db, "job-jsm", "fidelity_finding", "filter-discovery:A") {
		t.Fatal("expected a filter-discovery finding when listing fails")
	}
	var metadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'fidelity_finding' AND jira_id = 'filter-discovery:A'
	`).Scan(&metadataJSON); err != nil {
		t.Fatalf("load finding: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode finding metadata: %v", err)
	}
	if metadata["code"] != "jira_saved_filters_unavailable" || metadata["disposition"] != "failed" {
		t.Fatalf("finding metadata = %#v, want jira_saved_filters_unavailable/failed", metadata)
	}
}

// TestImportJiraBoardsReuseImportedFilterCollection covers board/favorite
// overlap: a board backed by an already-imported filter reuses that collection
// instead of creating a duplicate.
func TestImportJiraBoardsReuseImportedFilterCollection(t *testing.T) {
	h, db := newJiraFilterScopeHandler(t)
	workspace := createFilterScopeWorkspace(t, db, "Workspace A", "WSA")

	client := filterClientWith(jira.JiraFilter{ID: "500", Name: "Board filter", JQL: `project = A`})
	h.importJiraSavedFilters(context.Background(), "job-jsm", "A", workspace, []string{"A"}, client, 0)
	filterQL := workspaceCollectionQL(t, db, workspace)

	client.boards = []jira.JiraBoard{{ID: 7, Name: "A Board", Type: "scrum"}}
	client.configs = map[int]*jira.JiraBoardConfiguration{7: boardConfigurationWithFilter(t, 7, "500")}
	h.importJiraBoards(context.Background(), "job-jsm", "A", workspace, []string{"A"}, map[string]int{}, client, 0)

	if got := countWorkspaceCollections(t, db, workspace); got != 1 {
		t.Fatalf("collections = %d, want 1 (board must reuse the filter collection)", got)
	}
	if got := workspaceCollectionQL(t, db, workspace); got != filterQL {
		t.Fatalf("ql = %q, want the imported filter selector %q", got, filterQL)
	}
	if mappingExists(t, db, "job-jsm", "collection", "board:7") {
		t.Fatal("board must not create a second collection when its filter was already imported")
	}
}
