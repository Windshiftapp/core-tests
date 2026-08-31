package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

type v1BatchItemLinks struct {
	ItemID   int `json:"item_id"`
	Outgoing []struct {
		ID       int `json:"id"`
		TargetID int `json:"target_id"`
	} `json:"outgoing"`
	Incoming []struct {
		ID       int `json:"id"`
		SourceID int `json:"source_id"`
	} `json:"incoming"`
	HasMoreLinks    bool `json:"has_more_links"`
	NextAfterLinkID int  `json:"next_after_link_id"`
}

type v1BatchLinksPage struct {
	Data       []v1BatchItemLinks `json:"data"`
	Pagination struct {
		Page       int  `json:"page"`
		Limit      int  `json:"limit"`
		Total      int  `json:"total"`
		TotalPages int  `json:"total_pages"`
		HasMore    bool `json:"has_more"`
	} `json:"pagination"`
}

func TestV1LinksBatchSelectsPaginatedItemsWithCQL(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	key := shortKey("VLB")
	workspaceID, _ := CreateTestWorkspace(t, server, "V1 links batch", key)
	titlePrefix := "V1 links batch " + key
	itemA := CreateTestItem(t, server, workspaceID, titlePrefix+" A")
	itemB := CreateTestItem(t, server, workspaceID, titlePrefix+" B")
	itemC := CreateTestItem(t, server, workspaceID, titlePrefix+" C")

	createResponse := MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]interface{}{
		"link_type_id": 2,
		"source_type":  "item",
		"source_id":    itemA,
		"target_type":  "item",
		"target_id":    itemB,
	})
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var createdLink map[string]interface{}
	DecodeJSON(t, createResponse, &createdLink)
	createResponse.Body.Close()
	linkID := ExtractIDFromResponse(t, createdLink)

	query := url.QueryEscape(fmt.Sprintf(`title ~ "%s"`, titlePrefix))
	firstResponse := MakeBearerRequest(t, server, http.MethodGet,
		"/rest/api/v1/links/batch?ql="+query+"&page=1&limit=2&sort=key&order=asc", nil)
	defer firstResponse.Body.Close()
	AssertStatusCode(t, firstResponse, http.StatusOK)
	var firstPage v1BatchLinksPage
	DecodeJSON(t, firstResponse, &firstPage)
	if len(firstPage.Data) != 2 {
		t.Fatalf("first CQL page items = %d, want 2", len(firstPage.Data))
	}
	if firstPage.Pagination.Page != 1 || firstPage.Pagination.Limit != 2 ||
		firstPage.Pagination.Total != 3 || firstPage.Pagination.TotalPages != 2 || !firstPage.Pagination.HasMore {
		t.Fatalf("first CQL pagination = %+v, want page 1 of 2 with 3 total", firstPage.Pagination)
	}

	secondResponse := MakeBearerRequest(t, server, http.MethodGet,
		"/rest/api/v1/links/batch?ql="+query+"&page=2&limit=2&sort=key&order=asc", nil)
	defer secondResponse.Body.Close()
	AssertStatusCode(t, secondResponse, http.StatusOK)
	var secondPage v1BatchLinksPage
	DecodeJSON(t, secondResponse, &secondPage)
	if len(secondPage.Data) != 1 {
		t.Fatalf("second CQL page items = %d, want 1", len(secondPage.Data))
	}
	if secondPage.Pagination.Page != 2 || secondPage.Pagination.Total != 3 || secondPage.Pagination.HasMore {
		t.Fatalf("second CQL pagination = %+v, want final page with 3 total", secondPage.Pagination)
	}

	byItemID := make(map[int]v1BatchItemLinks, 3)
	for _, entry := range append(firstPage.Data, secondPage.Data...) {
		byItemID[entry.ItemID] = entry
	}
	if len(byItemID) != 3 {
		t.Fatalf("CQL anchor ids = %v, want %d, %d, %d", batchAnchorIDs(byItemID), itemA, itemB, itemC)
	}
	if got := byItemID[itemA].Outgoing; len(got) != 1 || got[0].ID != linkID || got[0].TargetID != itemB {
		t.Fatalf("item A outgoing = %+v, want link %d to item %d", got, linkID, itemB)
	}
	if got := byItemID[itemB].Incoming; len(got) != 1 || got[0].ID != linkID || got[0].SourceID != itemA {
		t.Fatalf("item B incoming = %+v, want link %d from item %d", got, linkID, itemA)
	}
	if got := byItemID[itemC]; len(got.Outgoing) != 0 || len(got.Incoming) != 0 {
		t.Fatalf("unlinked item C = %+v, want empty link lists", got)
	}
	for _, entry := range byItemID {
		if entry.HasMoreLinks || entry.NextAfterLinkID != 0 {
			t.Fatalf("uncapped item %d continuation = has_more:%t cursor:%d, want none",
				entry.ItemID, entry.HasMoreLinks, entry.NextAfterLinkID)
		}
	}
}

func TestV1LinksBatchExplicitIDsPreserveOrderAndValidateBounds(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "V1 explicit links batch", shortKey("VLE"))
	itemA := CreateTestItem(t, server, workspaceID, "Explicit link item A")
	itemB := CreateTestItem(t, server, workspaceID, "Explicit link item B")
	itemC := CreateTestItem(t, server, workspaceID, "Explicit link item C")

	response := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v1/links/batch?ids=%d,%d,%d,%d", itemB, itemA, itemB, itemC), nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var page v1BatchLinksPage
	DecodeJSON(t, response, &page)
	if len(page.Data) != 3 || page.Data[0].ItemID != itemB || page.Data[1].ItemID != itemA || page.Data[2].ItemID != itemC {
		t.Fatalf("explicit anchor order = %+v, want [%d %d %d]", page.Data, itemB, itemA, itemC)
	}
	if page.Pagination.Page != 1 || page.Pagination.Limit != 100 || page.Pagination.Total != 3 || page.Pagination.TotalPages != 1 {
		t.Fatalf("explicit pagination = %+v, want one page with 3 total", page.Pagination)
	}

	t.Run("requires items read scope", func(t *testing.T) {
		token := createTokenWithScopesAsUser(t, server, "admin", "testpass123", []string{"workspaces:read"})
		deniedResponse := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/links/batch?ids=%d", itemA), nil)
		defer deniedResponse.Body.Close()
		AssertStatusCode(t, deniedResponse, http.StatusForbidden)
	})

	for name, path := range map[string]string{
		"missing selector":        "/rest/api/v1/links/batch",
		"both selectors":          fmt.Sprintf("/rest/api/v1/links/batch?ids=%d&ql=id%%20%%3D%%20%d", itemA, itemA),
		"CQL continuation":        fmt.Sprintf("/rest/api/v1/links/batch?ql=id%%20%%3D%%20%d&after_id=1", itemA),
		"multi-item continuation": fmt.Sprintf("/rest/api/v1/links/batch?ids=%d,%d&after_id=1", itemA, itemB),
	} {
		t.Run(name, func(t *testing.T) {
			invalidResponse := MakeBearerRequest(t, server, http.MethodGet, path, nil)
			defer invalidResponse.Body.Close()
			AssertStatusCode(t, invalidResponse, http.StatusBadRequest)
		})
	}

	tooManyIDs := make([]string, 101)
	for i := range tooManyIDs {
		tooManyIDs[i] = strconv.Itoa(i + 1)
	}
	tooManyResponse := MakeBearerRequest(t, server, http.MethodGet,
		"/rest/api/v1/links/batch?ids="+strings.Join(tooManyIDs, ","), nil)
	defer tooManyResponse.Body.Close()
	AssertStatusCode(t, tooManyResponse, http.StatusBadRequest)
}

func batchAnchorIDs(entries map[int]v1BatchItemLinks) []int {
	ids := make([]int, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	return ids
}
