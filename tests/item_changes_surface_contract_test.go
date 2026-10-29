package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

type sessionItemChangesResponse struct {
	ChangedItemIDs     []int `json:"changed_item_ids"`
	RemovedItemIDs     []int `json:"removed_item_ids"`
	Watermark          int64 `json:"watermark"`
	RequiresFullReload bool  `json:"requires_full_reload"`
	MembershipDirty    bool  `json:"membership_dirty"`
}

func TestItemChangesSurfacesShareLogWithDistinctCursorContracts(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Item changes surfaces", shortKey("ICS"))
	itemID := CreateTestItem(t, server, workspaceID, "Shared change log")

	sessionPrime := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/items/changes?workspace_id=%d", workspaceID), nil)
	defer sessionPrime.Body.Close()
	AssertStatusCode(t, sessionPrime, http.StatusOK)
	var sessionBaseline sessionItemChangesResponse
	DecodeJSON(t, sessionPrime, &sessionBaseline)

	bearerBaseline := getV1ItemChanges(t, server, workspaceID, url.Values{})
	createV1Comment(t, server, itemID, "shared log mutation")

	sessionDelta := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/items/changes?workspace_id=%d&since=%d", workspaceID, sessionBaseline.Watermark), nil)
	defer sessionDelta.Body.Close()
	AssertStatusCode(t, sessionDelta, http.StatusOK)
	var sessionResult sessionItemChangesResponse
	DecodeJSON(t, sessionDelta, &sessionResult)
	if len(sessionResult.ChangedItemIDs) != 1 || sessionResult.ChangedItemIDs[0] != itemID ||
		len(sessionResult.RemovedItemIDs) != 0 || sessionResult.RequiresFullReload || sessionResult.MembershipDirty {
		t.Fatalf("session delta = %+v, want one visible changed item", sessionResult)
	}

	bearerResult := getV1ItemChanges(t, server, workspaceID, url.Values{
		"since": {bearerBaseline.NextCursor},
	})
	assertSingleItemUpsert(t, bearerResult, itemID)
	if bearerResult.Watermark != strconv.FormatInt(sessionResult.Watermark, 10) {
		t.Fatalf("bearer watermark = %q, session watermark = %d", bearerResult.Watermark, sessionResult.Watermark)
	}
}
