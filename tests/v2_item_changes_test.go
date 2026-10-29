package tests

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

type v2Changes struct {
	Changed   []int `json:"changed_item_ids"`
	Removed   []int `json:"removed_item_ids"`
	Watermark int64 `json:"watermark"`
	Next      int64 `json:"next_cursor"`
	More      bool  `json:"has_more"`
	Reset     bool  `json:"reset_required"`
	Reload    bool  `json:"requires_full_reload"`
	Dirty     bool  `json:"membership_dirty"`
}

func getV2Changes(t *testing.T, server *TestServer, workspace int, query string) v2Changes {
	t.Helper()
	response := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/changes?workspace_id=%d%s", workspace, query), nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var document struct {
		Data v2Changes `json:"data"`
	}
	DecodeJSON(t, response, &document)
	return document.Data
}

func TestV2ItemChangesFrozenPages(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspace, _ := CreateTestWorkspace(t, server, "Delta pages", shortKey("DPG"))
	item := CreateTestItem(t, server, workspace, "Repeated changes")
	baseline := getV2Changes(t, server, workspace, "")
	if baseline.Watermark == 0 || baseline.Next != baseline.Watermark || !reflect.DeepEqual(baseline.Changed, []int{}) || !reflect.DeepEqual(baseline.Removed, []int{}) || baseline.More || baseline.Reset || baseline.Reload || baseline.Dirty {
		t.Fatalf("invalid prime: %+v", baseline)
	}
	createV1Comment(t, server, item, "first")
	createV1Comment(t, server, item, "second")
	first := getV2Changes(t, server, workspace, fmt.Sprintf("&since=%d&limit=1", baseline.Next))
	if !reflect.DeepEqual(first.Changed, []int{item}) || len(first.Removed) != 0 || !first.More || first.Next <= baseline.Next || first.Next >= first.Watermark || first.Reset || first.Reload {
		t.Fatalf("invalid first page: %+v", first)
	}
	later := CreateTestItem(t, server, workspace, "After snapshot")
	other, _ := CreateTestWorkspace(t, server, "Other delta scope", shortKey("DOS"))
	CreateTestItem(t, server, other, "Other scope")
	second := getV2Changes(t, server, workspace, fmt.Sprintf("&since=%d&through=%d&limit=1", first.Next, first.Watermark))
	want := v2Changes{Changed: []int{item}, Removed: []int{}, Watermark: first.Watermark, Next: first.Watermark}
	if !reflect.DeepEqual(second, want) {
		t.Fatalf("final page = %+v, want %+v", second, want)
	}
	next := getV2Changes(t, server, workspace, fmt.Sprintf("&since=%d", second.Next))
	if !reflect.DeepEqual(next.Changed, []int{later}) || next.More || next.Reset || next.Reload || next.Next != next.Watermark {
		t.Fatalf("next snapshot: %+v", next)
	}
}

func TestV2ItemChangesResetAndValidation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspace, _ := CreateTestWorkspace(t, server, "Delta reset", shortKey("DRS"))
	CreateTestItem(t, server, workspace, "Baseline")
	baseline := getV2Changes(t, server, workspace, "")
	for _, query := range []string{fmt.Sprintf("&since=%d", baseline.Watermark+1), fmt.Sprintf("&since=0&through=%d", baseline.Watermark+1)} {
		got := getV2Changes(t, server, workspace, query)
		want := v2Changes{Changed: []int{}, Removed: []int{}, Watermark: baseline.Watermark, Next: baseline.Watermark, Reset: true, Reload: true, Dirty: true}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, want %+v", query, got, want)
		}
	}
	for _, query := range []string{"since=-1", "through=-1", "through=abc", "since=2&through=1", "limit=0", "limit=501", "limit=abc"} {
		t.Run(query, func(t *testing.T) {
			response := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/changes?workspace_id=%d&%s", workspace, query), nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusBadRequest)
		})
	}
	token := createTokenWithScopesAsUser(t, server, "admin", "testpass123", []string{"workspaces:read"})
	denied := MakeBearerRequestWithToken(t, server, token, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/changes?workspace_id=%d", workspace), nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusForbidden)
	var denial struct {
		Error struct {
			Code    string              `json:"code"`
			Message string              `json:"message"`
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	DecodeJSON(t, denied, &denial)
	if denial.Error.Code != "insufficient_permission" || denial.Error.Message != "Token lacks a required scope" || !reflect.DeepEqual(denial.Error.Details, map[string][]string{"required": {"items:read"}}) {
		t.Fatalf("scope denial = %+v", denial)
	}
}

func TestV2ItemChangesLargeWindowPreservesLegacyFallback(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspace, _ := CreateTestWorkspace(t, server, "Large delta window", shortKey("DLG"))
	item := CreateTestItem(t, server, workspace, "Busy item")
	baseline := getV2Changes(t, server, workspace, "")
	for i := range 501 {
		createV1Comment(t, server, item, fmt.Sprintf("Activity %d", i))
	}
	legacy := getV2Changes(t, server, workspace, fmt.Sprintf("&since=%d", baseline.Next))
	if !legacy.Reload || !legacy.Dirty || legacy.Reset {
		t.Fatalf("legacy overflow = %+v", legacy)
	}
	first := getV2Changes(t, server, workspace, fmt.Sprintf("&since=%d&limit=500", baseline.Next))
	if !first.More || first.Reload || first.Reset || !reflect.DeepEqual(first.Changed, []int{item}) {
		t.Fatalf("large first page = %+v", first)
	}
	last := getV2Changes(t, server, workspace, fmt.Sprintf("&since=%d&through=%d&limit=500", first.Next, first.Watermark))
	want := v2Changes{Changed: []int{item}, Removed: []int{}, Watermark: first.Watermark, Next: first.Watermark}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("large final page = %+v, want %+v", last, want)
	}
	empty := getV2Changes(t, server, workspace, fmt.Sprintf("&since=%d&through=%d&limit=1", last.Next, last.Watermark))
	want.Changed = []int{}
	if !reflect.DeepEqual(empty, want) {
		t.Fatalf("empty window = %+v, want %+v", empty, want)
	}
	response := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v1/items/%d", item), nil)
	response.Body.Close()
	AssertStatusCode(t, response, http.StatusNoContent)
	deleted := getV2Changes(t, server, workspace, fmt.Sprintf("&since=%d&limit=500", last.Next))
	if !reflect.DeepEqual(deleted.Changed, []int{}) || !reflect.DeepEqual(deleted.Removed, []int{item}) || !deleted.Dirty || deleted.Reload || deleted.Reset || deleted.More || deleted.Next != deleted.Watermark {
		t.Fatalf("deleted delta = %+v", deleted)
	}
}
