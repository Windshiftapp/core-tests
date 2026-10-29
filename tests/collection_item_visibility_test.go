package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCollectionItemReadsRequireCollectionVisibility(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	visibleWS, _ := CreateTestWorkspace(t, server, "Visible collection workspace", shortKey("CVV"))
	hiddenWS, _ := CreateTestWorkspace(t, server, "Hidden collection workspace", shortKey("CVH"))
	LockDownWorkspace(t, server, visibleWS)
	LockDownWorkspace(t, server, hiddenWS)
	visibleItem := CreateTestItem(t, server, visibleWS, "Visible matching item")
	CreateTestItem(t, server, hiddenWS, "Hidden matching item")
	readerID, username, password := CreateTestUserWithCredentials(t, server, "collection_visibility_reader", "collection_visibility_reader@example.com")
	AssignWorkspaceRole(t, server, readerID, visibleWS, "Viewer")
	session, bearer := CreateAuthCredentialsForUser(t, server, username, password)
	_, outsiderName, outsiderPassword := CreateTestUserWithCredentials(t, server, "collection_visibility_outsider", "collection_visibility_outsider@example.com")
	outsiderSession, outsiderBearer := CreateAuthCredentialsForUser(t, server, outsiderName, outsiderPassword)

	create := func(name string, workspaceID int, public, private bool, token string) int {
		t.Helper()
		body := map[string]any{"name": name, "ql_query": fmt.Sprintf("workspace_id IN (%d, %d)", visibleWS, hiddenWS), "is_public": public, "is_private": private}
		if public {
			body["public_slug"] = strings.ReplaceAll(strings.ToLower(name), " ", "-")
		}
		if workspaceID != 0 {
			body["workspace_id"] = workspaceID
		}
		response := MakeAuthRequestWithToken(t, server, token, http.MethodPost, "/v2/collections", body)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var document struct {
			Data struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		DecodeJSON(t, response, &document)
		return document.Data.ID
	}
	hidden := create("Hidden workspace filter", hiddenWS, false, false, server.SessionCookie)
	hiddenPublic := create("Shared hidden workspace filter", hiddenWS, true, false, server.SessionCookie)
	visible := create("Visible workspace filter", visibleWS, false, false, server.SessionCookie)
	private := create("Other owner's global filter", 0, false, true, server.SessionCookie)
	public := create("Shared global filter", 0, true, false, server.SessionCookie)
	owned := create("Reader's global filter", 0, false, false, session)

	for _, tc := range []struct {
		name         string
		collectionID int
		allowed      bool
		noWorkspaces bool
	}{
		{"hidden workspace", hidden, false, false},
		{"public flag does not bypass workspace", hiddenPublic, false, false},
		{"workspace viewer can use another owner's collection", visible, true, false},
		{"another owner's global collection", private, false, false},
		{"public global collection", public, true, false},
		{"own global collection", owned, true, false},
		{"missing collection", 2147483647, false, false},
		{"no workspace access still denies hidden collection", hidden, false, true},
		{"no workspace access can use public collection", public, true, true},
	} {
		for _, surface := range []string{"session", "bearer"} {
			for _, endpoint := range []string{"/items", "/items/backlog", "/items/changes"} {
				t.Run(tc.name+"/"+surface+endpoint, func(t *testing.T) {
					path := fmt.Sprintf("%s?collection_id=%d", endpoint, tc.collectionID)
					sessionToken, bearerToken := session, bearer
					if tc.noWorkspaces {
						sessionToken, bearerToken = outsiderSession, outsiderBearer
					}
					var response *http.Response
					if surface == "session" {
						response = makeSessionRequest(t, http.MethodGet, server.APIBase+"/v2"+path, sessionToken, nil, nil)
					} else {
						response = MakeBearerRequestWithToken(t, server, bearerToken, http.MethodGet, "/rest/api/v2"+path, nil)
					}
					defer response.Body.Close()
					if !tc.allowed {
						AssertStatusCode(t, response, http.StatusNotFound)
						if response.StatusCode != http.StatusNotFound {
							return
						}
						var document struct {
							Error struct {
								Code    string `json:"code"`
								Message string `json:"message"`
							} `json:"error"`
						}
						DecodeJSON(t, response, &document)
						if document.Error.Code != "not_found" || document.Error.Message != "Collection was not found" {
							t.Fatalf("denial = %+v, want not_found / Collection was not found", document)
						}
						return
					}
					AssertStatusCode(t, response, http.StatusOK)
					if endpoint == "/items/changes" {
						var document struct {
							Data struct {
								ChangedItemIDs     []int `json:"changed_item_ids"`
								RemovedItemIDs     []int `json:"removed_item_ids"`
								RequiresFullReload bool  `json:"requires_full_reload"`
								ResetRequired      bool  `json:"reset_required"`
								HasMore            bool  `json:"has_more"`
							} `json:"data"`
						}
						DecodeJSON(t, response, &document)
						if len(document.Data.ChangedItemIDs) != 0 || len(document.Data.RemovedItemIDs) != 0 || document.Data.RequiresFullReload || document.Data.ResetRequired || document.Data.HasMore {
							t.Fatalf("initial delta = %+v, want empty initial snapshot", document)
						}
						return
					}
					var document struct {
						Data []struct {
							ID int `json:"id"`
						} `json:"data"`
						Pagination struct {
							TotalItems int `json:"total_items"`
						} `json:"pagination"`
					}
					DecodeJSON(t, response, &document)
					if tc.noWorkspaces {
						if len(document.Data) != 0 || document.Pagination.TotalItems != 0 {
							t.Fatalf("expected empty authorized page, got %+v", document)
						}
					} else if len(document.Data) != 1 || document.Data[0].ID != visibleItem || document.Pagination.TotalItems != 1 {
						t.Fatalf("page = %+v, want only visible item %d", document, visibleItem)
					}
				})
			}
		}
	}
}
