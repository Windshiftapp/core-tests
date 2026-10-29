package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestPrivateCollectionUnpublishesPublicBoard covers the privacy toggle on a
// collection that was publicly shared: marking it private must clear the public
// slug and stop the unauthenticated public board from serving it.
func TestPrivateCollectionUnpublishesPublicBoard(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Private unpublish workspace", shortKey("PUP"))
	const slug = "private-unpublish-board"

	createResponse := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
		"name":         "Private unpublish",
		"ql_query":     fmt.Sprintf("workspace_id = %d", workspaceID),
		"workspace_id": workspaceID,
		"is_public":    true,
		"public_slug":  slug,
	})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, createResponse, &created)
	if created.Data.ID == 0 {
		t.Fatalf("collection id missing: %+v", created)
	}

	publicBoardStatus := func() int {
		t.Helper()
		response := makeRequest(t, http.MethodGet, server.BaseURL+"/api/public/board/"+slug, "", nil, nil)
		defer response.Body.Close()
		return response.StatusCode
	}

	if got := publicBoardStatus(); got != http.StatusOK {
		t.Fatalf("public board before private = %d, want 200", got)
	}

	patchResponse := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/collections/%d", created.Data.ID), map[string]any{"is_private": true})
	defer patchResponse.Body.Close()
	AssertStatusCode(t, patchResponse, http.StatusOK)
	var patched struct {
		Data struct {
			IsPublic   bool    `json:"is_public"`
			IsPrivate  bool    `json:"is_private"`
			PublicSlug *string `json:"public_slug"`
		} `json:"data"`
	}
	DecodeJSON(t, patchResponse, &patched)
	if !patched.Data.IsPrivate || patched.Data.IsPublic || patched.Data.PublicSlug != nil {
		t.Fatalf("after private: is_private=%v is_public=%v public_slug=%v",
			patched.Data.IsPrivate, patched.Data.IsPublic, patched.Data.PublicSlug)
	}

	if got := publicBoardStatus(); got != http.StatusNotFound {
		t.Fatalf("public board after private = %d, want 404", got)
	}
}

// TestUpdateSharingRejectsPublishForPrivateCollection covers the dedicated
// sharing endpoint: a private collection must not be published, and the request
// must fail with a validation error rather than storing a public flag the board
// would never serve.
func TestUpdateSharingRejectsPublishForPrivateCollection(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Private sharing workspace", shortKey("PSW"))

	createResponse := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
		"name":         "Private sharing",
		"ql_query":     fmt.Sprintf("workspace_id = %d", workspaceID),
		"workspace_id": workspaceID,
	})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, createResponse, &created)
	if created.Data.ID == 0 {
		t.Fatalf("collection id missing: %+v", created)
	}

	privateResponse := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/collections/%d", created.Data.ID), map[string]any{"is_private": true})
	AssertStatusCode(t, privateResponse, http.StatusOK)
	privateResponse.Body.Close()

	const slug = "private-sharing-board"
	sharingResponse := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/collections/%d/sharing", created.Data.ID), map[string]any{
		"is_public":   true,
		"public_slug": slug,
	})
	defer sharingResponse.Body.Close()
	AssertStatusCode(t, sharingResponse, http.StatusBadRequest)
	var apiError struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	DecodeJSON(t, sharingResponse, &apiError)
	if apiError.Error.Code != "invalid_request" {
		t.Fatalf("error code = %q, want invalid_request", apiError.Error.Code)
	}

	getResponse := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/collections/%d", created.Data.ID), nil)
	defer getResponse.Body.Close()
	AssertStatusCode(t, getResponse, http.StatusOK)
	var fetched struct {
		Data struct {
			IsPublic   bool    `json:"is_public"`
			IsPrivate  bool    `json:"is_private"`
			PublicSlug *string `json:"public_slug"`
		} `json:"data"`
	}
	DecodeJSON(t, getResponse, &fetched)
	if fetched.Data.IsPublic || !fetched.Data.IsPrivate || fetched.Data.PublicSlug != nil {
		t.Fatalf("after rejected publish: is_public=%v is_private=%v public_slug=%v",
			fetched.Data.IsPublic, fetched.Data.IsPrivate, fetched.Data.PublicSlug)
	}

	boardResponse := makeRequest(t, http.MethodGet, server.BaseURL+"/api/public/board/"+slug, "", nil, nil)
	defer boardResponse.Body.Close()
	if boardResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("public board = %d, want 404", boardResponse.StatusCode)
	}
}

// Public milestone swimlanes are part of the public sharing record. Both the
// dedicated sharing endpoint and the full collection patch must persist the
// flag, and an omitted flag must keep the stored value.
func TestCollectionSharingPersistsPublicMilestoneSwimlanes(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Public swimlane workspace", shortKey("PSL"))
	slug := "public-swimlane-theme"

	createResponse := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
		"name":         "Public swimlane collection",
		"ql_query":     fmt.Sprintf("workspace_id = %d", workspaceID),
		"workspace_id": workspaceID,
		"is_public":    true,
		"public_slug":  slug,
	})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, createResponse, &created)

	type sharingResponse struct {
		Data struct {
			IsPublic                 bool `json:"is_public"`
			PublicMilestoneSwimlanes bool `json:"public_milestone_swimlanes"`
		} `json:"data"`
	}

	sharingURL := fmt.Sprintf("/v2/collections/%d/sharing", created.Data.ID)
	enableResponse := MakeAuthRequest(t, server, http.MethodPatch, sharingURL, map[string]any{
		"is_public":                  true,
		"public_slug":                slug,
		"public_milestone_swimlanes": true,
	})
	defer enableResponse.Body.Close()
	AssertStatusCode(t, enableResponse, http.StatusOK)
	var enabled sharingResponse
	DecodeJSON(t, enableResponse, &enabled)
	if !enabled.Data.PublicMilestoneSwimlanes {
		t.Fatal("sharing response did not persist public_milestone_swimlanes")
	}

	// Omitting the field keeps the stored value instead of resetting it.
	keepResponse := MakeAuthRequest(t, server, http.MethodPatch, sharingURL, map[string]any{
		"is_public":   true,
		"public_slug": slug,
	})
	defer keepResponse.Body.Close()
	AssertStatusCode(t, keepResponse, http.StatusOK)
	var kept sharingResponse
	DecodeJSON(t, keepResponse, &kept)
	if !kept.Data.PublicMilestoneSwimlanes {
		t.Fatal("omitted public_milestone_swimlanes reset the stored value")
	}

	// The full collection patch used by the sharing dialog carries it too.
	fullURL := fmt.Sprintf("/v2/collections/%d", created.Data.ID)
	fullResponse := MakeAuthRequest(t, server, http.MethodPatch, fullURL, map[string]any{
		"public_milestone_swimlanes": false,
	})
	defer fullResponse.Body.Close()
	AssertStatusCode(t, fullResponse, http.StatusOK)
	var full sharingResponse
	DecodeJSON(t, fullResponse, &full)
	if full.Data.PublicMilestoneSwimlanes {
		t.Fatal("full collection patch did not persist public_milestone_swimlanes=false")
	}
}
