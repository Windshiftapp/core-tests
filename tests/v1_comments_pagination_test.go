package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestV1CommentsPaginationAndExpandedCommentCap(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "V1 comment pagination", shortKey("VCP"))
	itemID := CreateTestItem(t, server, workspaceID, "Paginated comments")

	for i := 1; i <= 26; i++ {
		response := MakeBearerRequestWithToken(
			t,
			server,
			server.BearerToken,
			http.MethodPost,
			fmt.Sprintf("/rest/api/v1/items/%d/comments", itemID),
			map[string]interface{}{"content": fmt.Sprintf("comment-%02d", i)},
		)
		AssertStatusCode(t, response, http.StatusCreated)
		response.Body.Close()
	}

	response := MakeBearerRequestWithToken(
		t,
		server,
		server.BearerToken,
		http.MethodGet,
		fmt.Sprintf("/rest/api/v1/items/%d/comments?page=2&limit=2&order=asc", itemID),
		nil,
	)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var page struct {
		Data []struct {
			Content string `json:"content"`
		} `json:"data"`
		Pagination struct {
			Page       int  `json:"page"`
			Limit      int  `json:"limit"`
			Total      int  `json:"total"`
			TotalPages int  `json:"total_pages"`
			HasMore    bool `json:"has_more"`
		} `json:"pagination"`
	}
	DecodeJSON(t, response, &page)
	if len(page.Data) != 2 ||
		page.Data[0].Content != "comment-03" ||
		page.Data[1].Content != "comment-04" {
		t.Fatalf("page data = %+v, want comment-03 and comment-04", page.Data)
	}
	if page.Pagination.Page != 2 ||
		page.Pagination.Limit != 2 ||
		page.Pagination.Total != 26 ||
		page.Pagination.TotalPages != 13 ||
		!page.Pagination.HasMore {
		t.Fatalf("pagination = %+v, want page 2 of 13 with more rows", page.Pagination)
	}

	expandedResponse := MakeBearerRequestWithToken(
		t,
		server,
		server.BearerToken,
		http.MethodGet,
		fmt.Sprintf("/rest/api/v1/items/%d?expand=comments", itemID),
		nil,
	)
	defer expandedResponse.Body.Close()
	AssertStatusCode(t, expandedResponse, http.StatusOK)
	var expanded struct {
		WorkspaceKey        string `json:"workspace_key"`
		WorkspaceItemNumber int    `json:"workspace_item_number"`
		Comments            []struct {
			Content string `json:"content"`
		} `json:"comments"`
	}
	DecodeJSON(t, expandedResponse, &expanded)
	if len(expanded.Comments) != 25 {
		t.Fatalf("expanded comments = %d, want newest 25", len(expanded.Comments))
	}
	if expanded.Comments[0].Content != "comment-26" ||
		expanded.Comments[len(expanded.Comments)-1].Content != "comment-02" {
		t.Fatalf("expanded comment bounds = %q..%q, want comment-26..comment-02",
			expanded.Comments[0].Content,
			expanded.Comments[len(expanded.Comments)-1].Content)
	}

	keyExpandedResponse := MakeBearerRequestWithToken(
		t,
		server,
		server.BearerToken,
		http.MethodGet,
		fmt.Sprintf("/rest/api/v1/workspaces/%s/items/%d?expand=comments", expanded.WorkspaceKey, expanded.WorkspaceItemNumber),
		nil,
	)
	defer keyExpandedResponse.Body.Close()
	AssertStatusCode(t, keyExpandedResponse, http.StatusOK)
	var keyExpanded struct {
		Comments []struct {
			Content string `json:"content"`
		} `json:"comments"`
	}
	DecodeJSON(t, keyExpandedResponse, &keyExpanded)
	if len(keyExpanded.Comments) != 25 {
		t.Fatalf("key-expanded comments = %d, want newest 25", len(keyExpanded.Comments))
	}
	if keyExpanded.Comments[0].Content != "comment-26" ||
		keyExpanded.Comments[len(keyExpanded.Comments)-1].Content != "comment-02" {
		t.Fatalf("key-expanded comment bounds = %q..%q, want comment-26..comment-02",
			keyExpanded.Comments[0].Content,
			keyExpanded.Comments[len(keyExpanded.Comments)-1].Content)
	}
}
