package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

type v1ItemChangesResponse struct {
	Changes []struct {
		ItemID     int    `json:"item_id"`
		ChangeType string `json:"change_type"`
	} `json:"changes"`
	NextCursor    string `json:"next_cursor"`
	Watermark     string `json:"watermark"`
	HasMore       bool   `json:"has_more"`
	ResetRequired bool   `json:"reset_required"`
}

func getV1ItemChanges(t *testing.T, server *TestServer, workspaceID int, values url.Values) v1ItemChangesResponse {
	t.Helper()
	values.Set("workspace_id", fmt.Sprintf("%d", workspaceID))
	response := MakeBearerRequestWithToken(
		t,
		server,
		server.BearerToken,
		http.MethodGet,
		"/rest/api/v1/items/changes?"+values.Encode(),
		nil,
	)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)

	var result v1ItemChangesResponse
	DecodeJSON(t, response, &result)
	return result
}

func createV1Comment(t *testing.T, server *TestServer, itemID int, content string) int {
	t.Helper()
	response := MakeBearerRequestWithToken(
		t,
		server,
		server.BearerToken,
		http.MethodPost,
		fmt.Sprintf("/rest/api/v1/items/%d/comments", itemID),
		map[string]interface{}{"content": content},
	)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusCreated)

	var comment struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, response, &comment)
	return comment.ID
}

func assertSingleItemUpsert(t *testing.T, response v1ItemChangesResponse, itemID int) {
	t.Helper()
	if len(response.Changes) != 1 {
		t.Fatalf("changes = %+v, want one item upsert", response.Changes)
	}
	change := response.Changes[0]
	if change.ItemID != itemID || change.ChangeType != "upsert" {
		t.Fatalf("change = %+v, want item %d upsert", change, itemID)
	}
	if response.HasMore || response.ResetRequired {
		t.Fatalf("response flags = has_more:%t reset_required:%t, want both false",
			response.HasMore, response.ResetRequired)
	}
	if response.NextCursor != response.Watermark {
		t.Fatalf("next_cursor = %q, watermark = %q, want equal", response.NextCursor, response.Watermark)
	}
}

func TestV1ItemChangesTrackCommentCreateUpdateAndDelete(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "V1 item changes", shortKey("VIC"))
	itemID := CreateTestItem(t, server, workspaceID, "Comment activity")

	noItemsToken := createTokenWithScopesAsUser(
		t,
		server,
		"admin",
		"testpass123",
		[]string{"workspaces:read"},
	)
	denied := MakeBearerRequestWithToken(
		t,
		server,
		noItemsToken,
		http.MethodGet,
		fmt.Sprintf("/rest/api/v1/items/changes?workspace_id=%d", workspaceID),
		nil,
	)
	denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusForbidden)

	baseline := getV1ItemChanges(t, server, workspaceID, url.Values{})
	if len(baseline.Changes) != 0 || baseline.NextCursor == "" {
		t.Fatalf("baseline = %+v, want an empty priming response with a cursor", baseline)
	}

	commentID := createV1Comment(t, server, itemID, "created")
	created := getV1ItemChanges(t, server, workspaceID, url.Values{
		"since": {baseline.NextCursor},
	})
	assertSingleItemUpsert(t, created, itemID)

	updateResponse := MakeBearerRequestWithToken(
		t,
		server,
		server.BearerToken,
		http.MethodPut,
		fmt.Sprintf("/rest/api/v1/comments/%d", commentID),
		map[string]interface{}{"content": "updated"},
	)
	updateResponse.Body.Close()
	AssertStatusCode(t, updateResponse, http.StatusOK)
	updated := getV1ItemChanges(t, server, workspaceID, url.Values{
		"since": {created.NextCursor},
	})
	assertSingleItemUpsert(t, updated, itemID)

	deleteResponse := MakeBearerRequestWithToken(
		t,
		server,
		server.BearerToken,
		http.MethodDelete,
		fmt.Sprintf("/rest/api/v1/comments/%d", commentID),
		nil,
	)
	deleteResponse.Body.Close()
	AssertStatusCode(t, deleteResponse, http.StatusNoContent)
	deleted := getV1ItemChanges(t, server, workspaceID, url.Values{
		"since": {updated.NextCursor},
	})
	assertSingleItemUpsert(t, deleted, itemID)

	deleteItemResponse := MakeBearerRequestWithToken(
		t,
		server,
		server.BearerToken,
		http.MethodDelete,
		fmt.Sprintf("/rest/api/v1/items/%d", itemID),
		nil,
	)
	deleteItemResponse.Body.Close()
	AssertStatusCode(t, deleteItemResponse, http.StatusNoContent)
	itemDeleted := getV1ItemChanges(t, server, workspaceID, url.Values{
		"since": {deleted.NextCursor},
	})
	if len(itemDeleted.Changes) != 1 ||
		itemDeleted.Changes[0].ItemID != itemID ||
		itemDeleted.Changes[0].ChangeType != "delete" {
		t.Fatalf("item deletion changes = %+v, want item %d delete", itemDeleted.Changes, itemID)
	}
}

func TestV1ItemChangesKeepsPaginationOnOneWatermark(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "V1 item change pages", shortKey("VIP"))
	itemID := CreateTestItem(t, server, workspaceID, "Paged activity")
	baseline := getV1ItemChanges(t, server, workspaceID, url.Values{})

	createV1Comment(t, server, itemID, "first")
	createV1Comment(t, server, itemID, "second")
	firstPage := getV1ItemChanges(t, server, workspaceID, url.Values{
		"since": {baseline.NextCursor},
		"limit": {"1"},
	})
	if len(firstPage.Changes) != 1 || !firstPage.HasMore {
		t.Fatalf("first page = %+v, want one change with has_more", firstPage)
	}

	createV1Comment(t, server, itemID, "after snapshot")
	secondPage := getV1ItemChanges(t, server, workspaceID, url.Values{
		"since":   {firstPage.NextCursor},
		"through": {firstPage.Watermark},
		"limit":   {"1"},
	})
	if len(secondPage.Changes) != 1 || secondPage.HasMore {
		t.Fatalf("second page = %+v, want final change in fixed snapshot", secondPage)
	}
	if secondPage.NextCursor != firstPage.Watermark {
		t.Fatalf("second next_cursor = %q, want snapshot watermark %q",
			secondPage.NextCursor, firstPage.Watermark)
	}

	nextSnapshot := getV1ItemChanges(t, server, workspaceID, url.Values{
		"since": {secondPage.NextCursor},
	})
	assertSingleItemUpsert(t, nextSnapshot, itemID)
}
