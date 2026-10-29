package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

// WI-1416: legacy personal workspaces created before 0.8.9 carry dashed keys
// (JOHN-DOE). Those keys cannot be created through the API any more — key
// validation rejects dashes by design — so this fixture renames an
// API-created personal workspace's key directly in the database. The legacy
// dashed shape is exactly the state under test.
func TestItemV2GetByKeyResolvesLegacyDashedWorkspaceKey(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	personal := MakeAuthRequest(t, server, http.MethodGet, "/workspaces/personal", nil)
	defer personal.Body.Close()
	AssertStatusCode(t, personal, http.StatusCreated)
	var ws struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, personal, &ws)

	if _, err := server.DB().Exec(`UPDATE workspaces SET key = 'JOHN-DOE' WHERE id = ?`, ws.ID); err != nil {
		t.Fatalf("rename personal workspace key to legacy dashed shape: %v", err)
	}

	itemID := CreateTestItem(t, server, ws.ID, "Legacy key target")
	detail := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%d", itemID), nil)
	defer detail.Body.Close()
	AssertStatusCode(t, detail, http.StatusOK)
	var created struct {
		WorkspaceItemNumber int `json:"workspace_item_number"`
	}
	DecodeJSON(t, detail, &created)
	if created.WorkspaceItemNumber < 1 {
		t.Fatalf("item %d has no workspace_item_number", itemID)
	}

	byKey := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/JOHN-DOE-%d", created.WorkspaceItemNumber), nil)
	defer byKey.Body.Close()
	AssertStatusCode(t, byKey, http.StatusOK)
	var resolved struct {
		ID  int    `json:"id"`
		Key string `json:"key"`
	}
	DecodeJSON(t, byKey, &resolved)
	if resolved.ID != itemID {
		t.Fatalf("dashed-key lookup returned id=%d, want %d", resolved.ID, itemID)
	}

	// A well-formed reference against a missing key/number is a 404, not a 400:
	// the reference parser accepts the dashed shape, existence is the lookup's job.
	missing := MakeAuthRequest(t, server, http.MethodGet, "/v2/items/JOHN-DOE-999999", nil)
	defer missing.Body.Close()
	AssertStatusCode(t, missing, http.StatusNotFound)
}

func TestItemV2GetResolvesIDBeforeKey(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, workspaceKey := CreateTestWorkspace(t, server, "Item Reference Lookup", shortKey("IRL"))
	itemID := CreateTestItem(t, server, workspaceID, "Reference target")

	numeric := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%d", itemID), nil)
	AssertStatusCode(t, numeric, http.StatusOK)
	var created struct {
		ID                  int `json:"id"`
		WorkspaceItemNumber int `json:"workspace_item_number"`
	}
	DecodeJSON(t, numeric, &created)
	numeric.Body.Close()
	if created.ID != itemID || created.WorkspaceItemNumber < 1 {
		t.Fatalf("numeric lookup returned %#v, want item %d with a workspace number", created, itemID)
	}

	itemKey := fmt.Sprintf("%s-%d", workspaceKey, created.WorkspaceItemNumber)
	for _, surface := range []struct {
		name string
		get  func() *http.Response
	}{
		{
			name: "session",
			get: func() *http.Response {
				return MakeAuthRequest(t, server, http.MethodGet, "/v2/items/"+itemKey, nil)
			},
		},
		{
			name: "bearer",
			get: func() *http.Response {
				return MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/items/"+itemKey, nil)
			},
		},
	} {
		t.Run(surface.name, func(t *testing.T) {
			response := surface.get()
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("key lookup status = %d, want 200; body=%s", response.StatusCode, body)
			}
			var item struct {
				ID  int    `json:"id"`
				Key string `json:"key"`
			}
			DecodeJSON(t, response, &item)
			if item.ID != itemID || item.Key != itemKey {
				t.Fatalf("key lookup returned %#v, want id=%d key=%q", item, itemID, itemKey)
			}
		})
	}

	paddedID := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%08d", itemID), nil)
	defer paddedID.Body.Close()
	AssertStatusCode(t, paddedID, http.StatusOK)
	var paddedItem struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, paddedID, &paddedItem)
	if paddedItem.ID != itemID {
		t.Fatalf("padded numeric lookup returned id=%d, want %d", paddedItem.ID, itemID)
	}

	missingID := MakeAuthRequest(t, server, http.MethodGet, "/v2/items/999999999", nil)
	defer missingID.Body.Close()
	AssertStatusCode(t, missingID, http.StatusNotFound)

	malformedKey := MakeAuthRequest(t, server, http.MethodGet, "/v2/items/IRL-not-a-number", nil)
	defer malformedKey.Body.Close()
	AssertStatusCode(t, malformedKey, http.StatusBadRequest)
	var invalid struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	DecodeJSON(t, malformedKey, &invalid)
	if invalid.Error.Code != "invalid_request" || invalid.Error.Details["field"] != "item_id" {
		t.Fatalf("malformed key error = %#v", invalid)
	}

	keyMutation := MakeAuthRequest(t, server, http.MethodPatch, "/v2/items/"+itemKey, map[string]any{"title": "must not change"})
	defer keyMutation.Body.Close()
	AssertStatusCode(t, keyMutation, http.StatusBadRequest)
}

func TestItemV2GetByKeyMasksInaccessibleItem(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, workspaceKey := CreateTestWorkspace(t, server, "Private Item Reference", shortKey("PIR"))
	LockDownWorkspace(t, server, workspaceID)
	itemID := CreateTestItem(t, server, workspaceID, "Private reference target")

	itemResponse := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%d", itemID), nil)
	AssertStatusCode(t, itemResponse, http.StatusOK)
	var item struct {
		WorkspaceItemNumber int `json:"workspace_item_number"`
	}
	DecodeJSON(t, itemResponse, &item)
	itemResponse.Body.Close()
	itemKey := fmt.Sprintf("%s-%d", workspaceKey, item.WorkspaceItemNumber)

	_, username, password := CreateTestUserWithCredentials(t, server, "item_reference_outsider", "item_reference_outsider@test.com")
	sessionCookie, bearerToken := CreateAuthCredentialsForUser(t, server, username, password)
	for _, surface := range []struct {
		name string
		get  func() *http.Response
	}{
		{
			name: "session",
			get: func() *http.Response {
				return MakeAuthRequestWithToken(t, server, sessionCookie, http.MethodGet, "/v2/items/"+itemKey, nil)
			},
		},
		{
			name: "bearer",
			get: func() *http.Response {
				return MakeBearerRequestWithToken(t, server, bearerToken, http.MethodGet, "/rest/api/v2/items/"+itemKey, nil)
			},
		},
	} {
		t.Run(surface.name, func(t *testing.T) {
			response := surface.get()
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusNotFound)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			DecodeJSON(t, response, &body)
			if body.Error.Code != "not_found" {
				t.Fatalf("inaccessible key error code = %q, want not_found", body.Error.Code)
			}
		})
	}
}
