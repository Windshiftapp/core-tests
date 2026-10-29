package tests

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestWSCLITimeWorklogAddLinksResolvedItemKey(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	projectID := createTimeProjectFixture(t, server, "CLI item key")
	workspaceID, workspaceKey := CreateTestWorkspace(t, server, "CLI worklog items", shortKey("CWK"))
	itemID := CreateTestItem(t, server, workspaceID, "CLI linked item")

	itemResponse := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/items/%d", itemID), nil)
	defer itemResponse.Body.Close()
	AssertStatusCode(t, itemResponse, http.StatusOK)
	var item struct {
		WorkspaceItemNumber int `json:"workspace_item_number"`
	}
	DecodeJSON(t, itemResponse, &item)
	itemKey := fmt.Sprintf("%s-%d", workspaceKey, item.WorkspaceItemNumber)

	_, stderr, code := runWS(t, server,
		"time", "worklog", "add",
		"--project-id", strconv.Itoa(projectID),
		"--description", "Resolved by CLI",
		"--date", "2026-09-05",
		"--duration", "1h",
		"--item-key", itemKey,
	)
	requireZero(t, code, stderr)

	response := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/time/worklogs?project_id=%d", projectID), nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var page struct {
		Data []struct {
			ItemID      *int   `json:"item_id"`
			Description string `json:"description"`
		} `json:"data"`
	}
	DecodeJSON(t, response, &page)
	if len(page.Data) != 1 || page.Data[0].ItemID == nil || *page.Data[0].ItemID != itemID || page.Data[0].Description != "Resolved by CLI" {
		t.Fatalf("persisted worklogs = %+v, want one entry linked to item %d", page.Data, itemID)
	}
}
