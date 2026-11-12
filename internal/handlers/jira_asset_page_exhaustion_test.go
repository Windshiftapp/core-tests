//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
)

// fakeAssetSearchClient serves scripted Assets object pages.
type fakeAssetSearchClient struct {
	jira.Client
	results []*jira.ObjectSearchResult
	calls   int
}

func (f *fakeAssetSearchClient) SearchObjects(context.Context, jira.ObjectSearchOptions) (*jira.ObjectSearchResult, error) {
	if f.calls >= len(f.results) {
		return &jira.ObjectSearchResult{IsLast: true}, nil
	}
	result := f.results[f.calls]
	f.calls++
	return result, nil
}

// TestImportJiraAssetObjectsReportsEmptyNonFinalPage is the WI-2070 regression:
// an empty page the source does not mark final cannot prove exhaustion, so it
// must record an incomplete finding instead of returning a partial inventory.
func TestImportJiraAssetObjectsReportsEmptyNonFinalPage(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-asset-page-secret", "", blobstore.Disabled())
	client := &fakeAssetSearchClient{results: []*jira.ObjectSearchResult{
		{ObjectEntries: nil, StartIndex: 0, ToIndex: 0, IsLast: false},
	}}

	h.importJiraAssetObjectsForType(context.Background(), "job-jsm", client, 1, 1, 0, "1", "1", nil, nil)

	var metadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'fidelity_finding' AND jira_id = 'asset-object-page:1:1'
	`).Scan(&metadataJSON); err != nil {
		t.Fatalf("load asset page finding: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode asset page finding: %v", err)
	}
	if metadata["code"] != "jira_asset_object_page_incomplete" || metadata["disposition"] != "incomplete" {
		t.Fatalf("finding metadata = %#v, want jira_asset_object_page_incomplete/incomplete", metadata)
	}
}

// TestJiraNextAssetObjectOffsetAdvancesByConsumedRows is the WI-2070
// regression: a server-capped short non-final page must advance the walk by the
// rows it actually returned instead of by the requested page size.
func TestJiraNextAssetObjectOffsetAdvancesByConsumedRows(t *testing.T) {
	t.Parallel()

	entries := func(n int) []jira.AssetObject {
		out := make([]jira.AssetObject, n)
		return out
	}

	tests := []struct {
		name        string
		startAt     int
		result      *jira.ObjectSearchResult
		wantNext    int
		wantAdvance bool
	}{
		{
			name:    "short first page advances by returned rows",
			startAt: 0,
			result: &jira.ObjectSearchResult{
				ObjectEntries: entries(25), StartIndex: 0, ToIndex: 25,
			},
			wantNext:    25,
			wantAdvance: true,
		},
		{
			name:    "short later page advances by returned rows",
			startAt: 25,
			result: &jira.ObjectSearchResult{
				ObjectEntries: entries(25), StartIndex: 25, ToIndex: 50,
			},
			wantNext:    50,
			wantAdvance: true,
		},
		{
			name:    "response offset behind the request is rejected",
			startAt: 25,
			result: &jira.ObjectSearchResult{
				ObjectEntries: entries(25), StartIndex: 0, ToIndex: 0,
			},
			wantAdvance: false,
		},
		{
			name:    "non-advancing page is rejected",
			startAt: 25,
			result: &jira.ObjectSearchResult{
				ObjectEntries: entries(0), StartIndex: 25, ToIndex: 25,
			},
			wantAdvance: false,
		},
		{
			name:        "nil result is rejected",
			startAt:     0,
			result:      nil,
			wantAdvance: false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			next, advanced := jiraNextAssetObjectOffset(test.startAt, test.result)
			if advanced != test.wantAdvance {
				t.Fatalf("advanced = %t, want %t", advanced, test.wantAdvance)
			}
			if advanced && next != test.wantNext {
				t.Fatalf("next = %d, want %d", next, test.wantNext)
			}
		})
	}
}
