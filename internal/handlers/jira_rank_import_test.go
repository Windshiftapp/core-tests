package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/cql"
	"windshift/internal/jira"
	"windshift/internal/repository"
)

type fakeJiraRankIssueKeyClient struct {
	jira.Client
	calls        []string
	rankedKeys   []string
	fallbackKeys []string
	rankedErr    error
	fallbackErr  error
}

func (f *fakeJiraRankIssueKeyClient) GetAllIssueKeys(_ context.Context, jql string) ([]string, error) {
	f.calls = append(f.calls, jql)
	if strings.Contains(jql, "ORDER BY Rank ASC") {
		return f.rankedKeys, f.rankedErr
	}
	return f.fallbackKeys, f.fallbackErr
}

func TestGetJiraIssueKeysInImportOrderUsesRankAndDeterministicTies(t *testing.T) {
	t.Parallel()

	client := &fakeJiraRankIssueKeyClient{rankedKeys: []string{"SP-3", "SP-1", "SP-2"}}
	got, err := getJiraIssueKeysInImportOrder(t.Context(), client, "SP", true)
	if err != nil {
		t.Fatalf("get ranked Jira issue keys: %v", err)
	}
	if !reflect.DeepEqual(got, client.rankedKeys) {
		t.Fatalf("ranked keys = %#v, want %#v", got, client.rankedKeys)
	}
	wantJQL := "project = SP AND statusCategory != Done ORDER BY Rank ASC, created ASC, key ASC"
	if !reflect.DeepEqual(client.calls, []string{wantJQL}) {
		t.Fatalf("JQL calls = %#v, want %#v", client.calls, []string{wantJQL})
	}
}

func TestGetJiraIssueKeysInImportOrderFallsBackWhenRankIsUnavailable(t *testing.T) {
	t.Parallel()

	client := &fakeJiraRankIssueKeyClient{
		rankedErr:    errors.New("field 'Rank' does not exist"),
		fallbackKeys: []string{"CORE-1", "CORE-2"},
	}
	got, err := getJiraIssueKeysInImportOrder(t.Context(), client, "CORE", false)
	if err != nil {
		t.Fatalf("get fallback Jira issue keys: %v", err)
	}
	if !reflect.DeepEqual(got, client.fallbackKeys) {
		t.Fatalf("fallback keys = %#v, want %#v", got, client.fallbackKeys)
	}
	wantCalls := []string{
		"project = CORE ORDER BY Rank ASC, created ASC, key ASC",
		"project = CORE ORDER BY created ASC, key ASC",
	}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("JQL calls = %#v, want %#v", client.calls, wantCalls)
	}
}

func TestGetJiraIssueKeysInImportOrderReportsRankAndFallbackFailures(t *testing.T) {
	t.Parallel()

	client := &fakeJiraRankIssueKeyClient{
		rankedErr:   errors.New("rank query failed"),
		fallbackErr: errors.New("fallback query failed"),
	}
	_, err := getJiraIssueKeysInImportOrder(t.Context(), client, "CORE", false)
	if err == nil {
		t.Fatal("expected issue-key query failure")
	}
	if !strings.Contains(err.Error(), "rank query failed") || !strings.Contains(err.Error(), "fallback query failed") {
		t.Fatalf("combined error = %q, want both failures", err)
	}
}

func TestJiraRankOrderGeneratesFractionalIndexesAndSearchableJiraKeys(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-rank-import-test-secret", "", blobstore.Disabled())

	var workspaceID, itemTypeID, statusID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active)
		VALUES ('Storymap Premium', 'SP', true)
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("find item type: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM statuses ORDER BY id LIMIT 1`).Scan(&statusID); err != nil {
		t.Fatalf("find status: %v", err)
	}

	jiraKeyFieldID, err := handler.ensureJiraIssueKeyCustomField("job-jsm")
	if err != nil {
		t.Fatalf("ensure Jira Key field: %v", err)
	}
	reusedFieldID, err := handler.ensureJiraIssueKeyCustomField("job-jsm")
	if err != nil {
		t.Fatalf("reuse Jira Key field: %v", err)
	}
	if reusedFieldID != jiraKeyFieldID {
		t.Fatalf("reused Jira Key field = %d, want %d", reusedFieldID, jiraKeyFieldID)
	}

	fetched := []jira.JiraIssue{
		{ID: "10003", Key: "SP-3", Fields: jira.JiraIssueFields{Summary: "Third", IssueType: &jira.JiraIssueType{ID: "type"}, Status: &jira.JiraStatus{ID: "status"}}},
		{ID: "10001", Key: "SP-1", Fields: jira.JiraIssueFields{Summary: "First", IssueType: &jira.JiraIssueType{ID: "type"}, Status: &jira.JiraStatus{ID: "status"}}},
		{ID: "10002", Key: "SP-2", Fields: jira.JiraIssueFields{Summary: "Second", IssueType: &jira.JiraIssueType{ID: "type"}, Status: &jira.JiraStatus{ID: "status"}}},
	}
	rankedKeys := []string{"SP-1", "SP-2", "SP-3"}
	sortJiraIssuesByRequestedKeyOrder(fetched, rankedKeys)
	for index := range fetched {
		im := &jiraImportContext{
			jobID:             "job-jsm",
			progress:          &ImportProgress{},
			workspaceID:       workspaceID,
			statusMap:         map[string]int{"status": statusID},
			itemTypeMap:       map[string]int{"type": itemTypeID},
			userMap:           map[string]int{},
			usernameMap:       map[string]string{},
			portalCustomerMap: map[string]int{},
			versionMap:        map[string]int{},
			iterationMap:      map[string]int{},
			customFieldIDMap:  map[string]int{jiraIssueKeyFieldSourceID: jiraKeyFieldID},
			choiceOptionIDs:   map[string]map[string]int{},
		}
		if err := handler.importIssue(t.Context(), im, &fetched[index]); err != nil {
			t.Fatalf("import issue %s: %v", fetched[index].Key, err)
		}
	}

	rows, err := db.Query(`
		SELECT title, frac_index, custom_field_values
		FROM items
		WHERE workspace_id = ?
		ORDER BY frac_index
	`, workspaceID)
	if err != nil {
		t.Fatalf("load imported issue order: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var titles, fracIndexes []string
	for rows.Next() {
		var title, fracIndex, customFieldJSON string
		if err := rows.Scan(&title, &fracIndex, &customFieldJSON); err != nil {
			t.Fatalf("scan imported issue: %v", err)
		}
		var values map[string]any
		if err := json.Unmarshal([]byte(customFieldJSON), &values); err != nil {
			t.Fatalf("decode custom fields for %s: %v", title, err)
		}
		expectedKey := "SP-" + strconv.Itoa(len(titles)+1)
		if got := values[strconv.Itoa(jiraKeyFieldID)]; got != expectedKey {
			t.Fatalf("%s Jira Key = %#v, want %q", title, got, expectedKey)
		}
		titles = append(titles, title)
		fracIndexes = append(fracIndexes, fracIndex)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate imported issue order: %v", err)
	}
	if want := []string{"First", "Second", "Third"}; !reflect.DeepEqual(titles, want) {
		t.Fatalf("titles by frac_index = %#v, want %#v", titles, want)
	}
	for index := 1; index < len(fracIndexes); index++ {
		if fracIndexes[index-1] >= fracIndexes[index] {
			t.Fatalf("frac indexes are not increasing: %#v", fracIndexes)
		}
	}

	customFields, err := repository.NewItemRepository(db).GetCQLCustomFieldMap()
	if err != nil {
		t.Fatalf("load CQL custom fields: %v", err)
	}
	whereSQL, args, err := cql.NewEvaluator(nil, customFields, db.GetDriverName()).
		EvaluateToSQL("`cf_Jira Key` = \"SP-2\"")
	if err != nil {
		t.Fatalf("compile Jira Key query: %v", err)
	}
	var matchedTitle string
	if err := db.QueryRow("SELECT i.title FROM items i WHERE "+whereSQL, args...).Scan(&matchedTitle); err != nil {
		t.Fatalf("query imported issue by Jira Key: %v", err)
	}
	if matchedTitle != "Second" {
		t.Fatalf("Jira Key query matched %q, want Second", matchedTitle)
	}
}
