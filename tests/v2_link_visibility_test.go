package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestV2BatchLinksConcealsHiddenTestCaseOnBothMounts(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	visible, _ := CreateTestWorkspace(t, server, "Visible anchor", shortKey("LVA"))
	hidden, _ := CreateTestWorkspace(t, server, "Hidden test", shortKey("LVH"))
	LockDownWorkspace(t, server, visible)
	LockDownWorkspace(t, server, hidden)
	itemID := CreateTestItem(t, server, visible, "Anchor")
	caseID := seedTestCase(t, server, hidden, "Confidential case title")
	link := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/links", map[string]any{
		"source_type": "item", "source_id": itemID,
		"target_type": "test_case", "target_id": caseID,
		"link_type_id": linkTypeIDByBuiltinKey(t, server, "tests"),
	})
	defer link.Body.Close()
	AssertStatusCode(t, link, http.StatusCreated)
	userID, username, password := CreateTestUserWithCredentials(t, server, "link_reader", "link_reader@example.com")
	AssignWorkspaceRole(t, server, userID, visible, "Editor")
	cookie, token := CreateAuthCredentialsForUser(t, server, username, password)
	for _, mount := range []string{"session", "bearer"} {
		t.Run(mount, func(t *testing.T) {
			path := fmt.Sprintf("/v2/links/batch?ids=%d&page_size=1", itemID)
			var response *http.Response
			if mount == "session" {
				response = MakeAuthRequestWithToken(t, server, cookie, http.MethodGet, path, nil)
			} else {
				response = MakeBearerRequestWithToken(t, server, token, http.MethodGet, "/rest/api"+path, nil)
			}
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)
			var document struct {
				Data []map[string]any `json:"data"`
			}
			DecodeJSON(t, response, &document)
			want := []map[string]any{{"item_id": float64(itemID), "outgoing": []any{}, "incoming": []any{}, "has_more_links": false}}
			if !reflect.DeepEqual(document.Data, want) {
				t.Fatalf("visible batch = %+v, want %+v", document.Data, want)
			}
		})
	}
}

func TestV2BatchLinksEmptyQueryReturnsEmptyPage(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	response := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/links/batch?ql="+url.QueryEscape(`title = "No such anchor"`), nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var document map[string]any
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"data": []any{}, "pagination": map[string]any{"page": float64(1), "page_size": float64(50), "total_items": float64(0), "total_pages": float64(0)}}
	if !reflect.DeepEqual(document, want) {
		t.Fatalf("empty page = %+v, want %+v", document, want)
	}
}
