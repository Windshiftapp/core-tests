package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"windshift/internal/logger"
)

func TestPageApplication_CookieRESTAndMCPMutationAuditContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Page Application Contract", shortKey("PAC"))

	createResponse := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/pages", workspaceID), map[string]interface{}{
			"title":   "Shared page",
			"content": "cookie content",
		})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResponse, &created)
	pageID := ExtractIDFromResponse(t, created)

	updateResponse := MakeBearerRequest(t, server, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d", workspaceID, pageID), map[string]interface{}{
			"content": "REST content",
		})
	defer updateResponse.Body.Close()
	AssertStatusCode(t, updateResponse, http.StatusOK)
	var restUpdated map[string]interface{}
	DecodeJSON(t, updateResponse, &restUpdated)
	if restUpdated["title"] != "Shared page" || restUpdated["content"] != "REST content" {
		t.Fatalf("REST partial update = title %v content %v", restUpdated["title"], restUpdated["content"])
	}

	mcpBody, err := callMCPForContract(dialMCPWithToken(t, server, server.BearerToken), "update_page", map[string]interface{}{
		"page_id": pageID,
		"title":   "MCP title",
	})
	if err != nil {
		t.Fatalf("MCP update_page: %v", err)
	}
	if !strings.Contains(mcpBody, "MCP title") || !strings.Contains(mcpBody, "REST content") {
		t.Fatalf("MCP partial update response = %q", mcpBody)
	}

	archiveResponse := MakeAuthRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, pageID), nil)
	defer archiveResponse.Body.Close()
	AssertStatusCode(t, archiveResponse, http.StatusOK)

	var archivedAt interface{}
	if err := server.server.DB().QueryRow(`SELECT archived_at FROM pages WHERE id = ?`, pageID).Scan(&archivedAt); err != nil {
		t.Fatalf("load archived page: %v", err)
	}
	if archivedAt == nil {
		t.Fatal("page was not archived")
	}

	rows, err := server.server.DB().Query(`
		SELECT action_type, details
		FROM audit_logs
		WHERE resource_type = ? AND resource_id = ?
		ORDER BY id
	`, logger.ResourcePage, pageID)
	if err != nil {
		t.Fatalf("query page audits: %v", err)
	}
	defer rows.Close()

	var actions []string
	var details []map[string]interface{}
	for rows.Next() {
		var action, detailsJSON string
		if err := rows.Scan(&action, &detailsJSON); err != nil {
			t.Fatalf("scan page audit: %v", err)
		}
		var detail map[string]interface{}
		if err := json.Unmarshal([]byte(detailsJSON), &detail); err != nil {
			t.Fatalf("decode page audit details: %v", err)
		}
		actions = append(actions, action)
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate page audits: %v", err)
	}

	wantActions := []string{
		logger.ActionPageCreate,
		logger.ActionPageUpdate,
		logger.ActionPageUpdate,
		logger.ActionPageArchive,
	}
	if len(actions) != len(wantActions) {
		t.Fatalf("page audit actions = %v, want %v", actions, wantActions)
	}
	for i := range wantActions {
		if actions[i] != wantActions[i] {
			t.Fatalf("page audit action[%d] = %q, want %q", i, actions[i], wantActions[i])
		}
	}
	if details[0]["auth_method"] != "cookie" || details[1]["auth_method"] != "bearer" ||
		details[2]["source"] != "mcp" || details[3]["auth_method"] != "cookie" {
		t.Fatalf("page audit attribution = %#v", details)
	}
	if details[1]["api_token_id"] == nil {
		t.Fatalf("REST audit lacks api_token_id: %#v", details[1])
	}
}

func TestPageApplication_ContentHashPreconditionAcrossCookieRESTAndMCP(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Page Content Hash Contract", shortKey("PCH"))

	createResponse := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/pages", workspaceID), map[string]interface{}{
			"title":   "Guarded page",
			"content": "original",
		})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResponse, &created)
	pageID := ExtractIDFromResponse(t, created)
	originalHash, _ := created["content_hash"].(string)
	if originalHash == "" {
		t.Fatal("create response omitted content_hash")
	}

	staleREST := MakeBearerRequest(t, server, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d", workspaceID, pageID), map[string]interface{}{
			"content":               "stale REST",
			"expected_content_hash": "stale",
		})
	defer staleREST.Body.Close()
	AssertStatusCode(t, staleREST, http.StatusConflict)

	matchingREST := MakeBearerRequest(t, server, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d", workspaceID, pageID), map[string]interface{}{
			"content":               "REST update",
			"expected_content_hash": originalHash,
		})
	defer matchingREST.Body.Close()
	AssertStatusCode(t, matchingREST, http.StatusOK)
	var restUpdated map[string]interface{}
	DecodeJSON(t, matchingREST, &restUpdated)
	restHash, _ := restUpdated["content_hash"].(string)
	if restHash == "" || restHash == originalHash {
		t.Fatalf("REST update content_hash = %q, want a new non-empty hash", restHash)
	}

	staleMCP, err := callMCPForContract(dialMCPWithToken(t, server, server.BearerToken), "update_page", map[string]interface{}{
		"page_id":               pageID,
		"content":               "stale MCP",
		"expected_content_hash": originalHash,
	})
	if err != nil {
		t.Fatalf("MCP stale update_page: %v", err)
	}
	if !strings.Contains(staleMCP, "page content changed since it was read") {
		t.Fatalf("MCP stale response = %q", staleMCP)
	}

	matchingMCP, err := callMCPForContract(dialMCPWithToken(t, server, server.BearerToken), "update_page", map[string]interface{}{
		"page_id":               pageID,
		"content":               "MCP update",
		"expected_content_hash": restHash,
	})
	if err != nil {
		t.Fatalf("MCP matching update_page: %v", err)
	}
	if !strings.Contains(matchingMCP, "MCP update") || !strings.Contains(matchingMCP, "content_hash") {
		t.Fatalf("MCP matching response = %q", matchingMCP)
	}

	var currentHash string
	if err := server.server.DB().QueryRow(`SELECT content_hash FROM pages WHERE id = ?`, pageID).Scan(&currentHash); err != nil {
		t.Fatalf("load current content hash: %v", err)
	}
	staleCookie := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, pageID), map[string]interface{}{
			"title":                 "Guarded page",
			"content":               "stale cookie",
			"expected_content_hash": restHash,
		})
	defer staleCookie.Body.Close()
	AssertStatusCode(t, staleCookie, http.StatusConflict)

	legacyCookie := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, pageID), map[string]interface{}{
			"title":   "Guarded page",
			"content": "legacy cookie update",
		})
	defer legacyCookie.Body.Close()
	AssertStatusCode(t, legacyCookie, http.StatusOK)

	var finalContent, finalHash string
	if err := server.server.DB().QueryRow(`SELECT content, content_hash FROM pages WHERE id = ?`, pageID).Scan(&finalContent, &finalHash); err != nil {
		t.Fatalf("load final page: %v", err)
	}
	if finalContent != "legacy cookie update" || finalHash == currentHash {
		t.Fatalf("final content/hash = %q/%q", finalContent, finalHash)
	}
}
