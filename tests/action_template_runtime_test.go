package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCloseSubtasksActionTemplateRuntime(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Close Subtasks Runtime", shortKey("CSR"))

	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/statuses", workspaceID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var statuses []struct {
		ID          int  `json:"id"`
		IsCompleted bool `json:"is_completed"`
		Category    *struct {
			IsCompleted bool `json:"is_completed"`
		} `json:"category"`
	}
	DecodeJSON(t, resp, &statuses)
	terminalStatusID := 0
	for _, status := range statuses {
		if status.IsCompleted || (status.Category != nil && status.Category.IsCompleted) {
			terminalStatusID = status.ID
			break
		}
	}
	if terminalStatusID == 0 {
		t.Fatal("workspace workflow has no completed status")
	}

	createItem := func(title string, parentID int) int {
		t.Helper()
		payload := map[string]any{"workspace_id": workspaceID, "title": title}
		if parentID > 0 {
			payload["parent_id"] = parentID
		}
		createResp := MakeAuthRequest(t, server, http.MethodPost, "/items", payload)
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var item map[string]any
		DecodeJSON(t, createResp, &item)
		return ExtractIDFromResponse(t, item)
	}

	parentID := createItem("parent", 0)
	childAID := createItem("child A", parentID)
	childBID := createItem("child B", parentID)

	applyResp := MakeAuthRequest(
		t,
		server,
		http.MethodPost,
		fmt.Sprintf("/workspaces/%d/action-templates/close_subtasks_on_parent_close/apply", workspaceID),
		nil,
	)
	defer applyResp.Body.Close()
	AssertStatusCode(t, applyResp, http.StatusCreated)
	var applied map[string]any
	DecodeJSON(t, applyResp, &applied)
	AssertJSONField(t, applied, "template_key", "close_subtasks_on_parent_close")

	transitionResp := MakeAuthRequest(
		t,
		server,
		http.MethodPost,
		fmt.Sprintf("/items/%d/transition", parentID),
		map[string]any{"to_status_id": terminalStatusID},
	)
	defer transitionResp.Body.Close()
	AssertStatusCode(t, transitionResp, http.StatusOK)

	itemStatus := func(itemID int) int {
		t.Helper()
		itemResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
		defer itemResp.Body.Close()
		AssertStatusCode(t, itemResp, http.StatusOK)
		var item struct {
			StatusID int `json:"status_id"`
		}
		DecodeJSON(t, itemResp, &item)
		return item.StatusID
	}

	waitForCondition(t, 10*time.Second, "close-subtasks action to transition both children", func() bool {
		return itemStatus(childAID) == terminalStatusID && itemStatus(childBID) == terminalStatusID
	})
}
