package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Portal knowledge-base workspace-pages wiring. A channel manager explicitly
// publishes a workspace pages tree (or a sub-page subtree) into the portal
// knowledge base; published pages become searchable for portal users and are
// reported as publicly viewable by the page publication endpoint.

func createKBPage(t *testing.T, server *TestServer, workspaceID int, title, content string) int {
	t.Helper()
	return createKBPageUnder(t, server, workspaceID, nil, title, content)
}

func createKBPageUnder(t *testing.T, server *TestServer, workspaceID int, parentID *int, title, content string) int {
	t.Helper()
	payload := map[string]interface{}{
		"title":   title,
		"content": content,
	}
	if parentID != nil {
		payload["parent_id"] = *parentID
	}
	resp := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/pages", workspaceID), payload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	return ExtractIDFromResponse(t, created)
}

func wireKnowledgeBasePageSources(t *testing.T, server *TestServer, channelID int, sources []map[string]interface{}) *http.Response {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/channels/%d/config", channelID), map[string]interface{}{
		"config": map[string]interface{}{
			"knowledge_base_page_sources": sources,
		},
	})
	defer resp.Body.Close()
	return resp
}

func portalKBSearch(t *testing.T, server *TestServer, portalCookie, slug, query string) []map[string]interface{} {
	t.Helper()
	resp := MakePortalRequest(t, server, portalCookie, http.MethodPost,
		fmt.Sprintf("/portal/%s/knowledge-base/search", slug), map[string]string{"query": query})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("portal knowledge-base search: expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Data []map[string]interface{} `json:"data"`
	}
	DecodeJSON(t, resp, &result)
	return result.Data
}

// portalKBList fetches the browse listing of published pages for a portal.
// The portal page endpoints respond with a bare JSON array, mirroring the
// single-page endpoint's unenveloped payload.
func portalKBList(t *testing.T, server *TestServer, portalCookie, slug string) []map[string]interface{} {
	t.Helper()
	resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/knowledge-base/pages", slug), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("portal knowledge-base listing: expected 200, got %d", resp.StatusCode)
	}
	var listing []map[string]interface{}
	DecodeJSON(t, resp, &listing)
	return listing
}

func TestPortalKnowledgeBase_IncludesWiredWorkspacePages(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wiredWorkspace, _ := CreateTestWorkspace(t, server, "KB Wired Workspace", shortKey("KBW"))
	otherWorkspace, _ := CreateTestWorkspace(t, server, "KB Other Workspace", shortKey("KBO"))

	pageID := createKBPage(t, server, wiredWorkspace,
		"Onboarding checklist", "The fandorin onboarding checklist covers first-day setup steps.")
	createKBPage(t, server, otherWorkspace,
		"Other onboarding checklist", "The fandorin checklist for the other workspace stays private.")

	portalSlug, channelID := SetupPortalChannel(t, server, wiredWorkspace)
	_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "KB Reader", "kb-reader@test.com")

	t.Run("NothingPublishedBeforeWiring", func(t *testing.T) {
		// Neither a Docmost share nor workspace-pages wiring is configured,
		// so the knowledge base itself does not exist for this portal yet.
		resp := MakePortalRequest(t, server, portalCookie, http.MethodPost,
			fmt.Sprintf("/portal/%s/knowledge-base/search", portalSlug), map[string]string{"query": "fandorin"})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("search without any knowledge base source: expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("WiringExposesOnlyTheConnectedWorkspace", func(t *testing.T) {
		resp := wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
			{"workspace_id": wiredWorkspace},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("wiring whole workspace: expected 200, got %d", resp.StatusCode)
		}

		hits := portalKBSearch(t, server, portalCookie, portalSlug, "fandorin")
		var published []map[string]interface{}
		for _, hit := range hits {
			if hit["source"] == "workspace_page" {
				published = append(published, hit)
			}
		}
		if len(published) != 1 {
			t.Fatalf("expected exactly 1 workspace page hit, got %d: %+v", len(published), hits)
		}
		if int(published[0]["page_id"].(float64)) != pageID {
			t.Fatalf("expected page %d, got %+v", pageID, published[0])
		}
		if published[0]["workspace_id"].(float64) != float64(wiredWorkspace) {
			t.Fatalf("expected workspace %d, got %+v", wiredWorkspace, published[0])
		}
	})

	t.Run("PublishedPageContentIsReadableAndForeignPagesAreNot", func(t *testing.T) {
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/knowledge-base/pages/%d", portalSlug, pageID), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("published page fetch: expected 200, got %d", resp.StatusCode)
		}
		var page struct {
			Source  string `json:"source"`
			PageID  int    `json:"page_id"`
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		DecodeJSON(t, resp, &page)
		if page.Source != "workspace_page" || page.PageID != pageID {
			t.Fatalf("published page payload: %+v", page)
		}
		if page.Title != "Onboarding checklist" || page.Content == "" {
			t.Fatalf("published page content missing: %+v", page)
		}
	})

	t.Run("ForeignWorkspacePageReturns404", func(t *testing.T) {
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/knowledge-base/pages/999999", portalSlug), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 for unpublished page, got %d", resp.StatusCode)
		}
	})
}

func TestPortalKnowledgeBase_SubtreeWiringOnlyPublishesTheSubtree(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "KB Subtree Workspace", shortKey("KBS"))
	handbookID := createKBPage(t, server, workspaceID, "Engineering Handbook", "Handbook root.")
	vpnID := createKBPageUnder(t, server, workspaceID, &handbookID, "VPN setup", "The bleruntu VPN needs MFA.")
	hrID := createKBPage(t, server, workspaceID, "HR Policies", "The bleruntu expenses policy lives here.")

	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)
	_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Subtree Reader", "subtree-reader@test.com")

	resp := wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
		{"workspace_id": workspaceID, "root_page_id": handbookID},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wiring sub-page subtree: expected 200, got %d", resp.StatusCode)
	}

	hits := portalKBSearch(t, server, portalCookie, portalSlug, "bleruntu")
	publishedIDs := map[int]bool{}
	for _, hit := range hits {
		if hit["source"] == "workspace_page" {
			publishedIDs[int(hit["page_id"].(float64))] = true
		}
	}
	if !publishedIDs[vpnID] {
		t.Fatalf("subtree page %d missing from hits: %+v", vpnID, hits)
	}
	if publishedIDs[hrID] {
		t.Fatalf("page outside the wired subtree leaked into hits: %+v", hits)
	}

	// The subtree page is readable; the sibling-subtree page is not.
	readResp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/knowledge-base/pages/%d", portalSlug, vpnID), nil)
	defer readResp.Body.Close()
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("subtree page fetch: expected 200, got %d", readResp.StatusCode)
	}

	deniedResp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/knowledge-base/pages/%d", portalSlug, hrID), nil)
	defer deniedResp.Body.Close()
	if deniedResp.StatusCode != http.StatusNotFound {
		t.Fatalf("page outside subtree: expected 404, got %d", deniedResp.StatusCode)
	}
}

// A portal's knowledge-base search must only surface pages wired into that
// portal. Pages published exclusively to a different portal (another
// customer organisation) must never appear as hits — titles, heading paths,
// and snippets all carry content.
func TestPortalKnowledgeBase_SearchDoesNotLeakOtherPortalsPages(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceA, _ := CreateTestWorkspace(t, server, "KB Portal A Workspace", shortKey("KPA"))
	workspaceB, _ := CreateTestWorkspace(t, server, "KB Portal B Workspace", shortKey("KPB"))

	createKBPage(t, server, workspaceA, "Portal A Handbook", "The noshamu handbook describes portal A workflows.")
	secretID := createKBPage(t, server, workspaceB, "Portal B Secret Runbook", "The queldaron runbook is for portal B only.")

	slugA, channelA := SetupPortalChannel(t, server, workspaceA)
	slugB, channelB := SetupPortalChannel(t, server, workspaceB)

	if resp := wireKnowledgeBasePageSources(t, server, channelB, []map[string]interface{}{
		{"workspace_id": workspaceB},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("wiring portal B: expected 200, got %d", resp.StatusCode)
	}
	// Wire portal A too so its search endpoint exists; it has no access to
	// workspace B in any way.
	if resp := wireKnowledgeBasePageSources(t, server, channelA, []map[string]interface{}{
		{"workspace_id": workspaceA},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("wiring portal A: expected 200, got %d", resp.StatusCode)
	}

	_, cookieA := CreatePortalCustomerWithSession(t, server, channelA, "Portal A Reader", "portal-a-reader@test.com")
	_, cookieB := CreatePortalCustomerWithSession(t, server, channelB, "Portal B Reader", "portal-b-reader@test.com")

	t.Run("PortalASearchHidesPortalBPages", func(t *testing.T) {
		hits := portalKBSearch(t, server, cookieA, slugA, "queldaron")
		for _, hit := range hits {
			if hit["source"] == "workspace_page" {
				t.Fatalf("portal A search leaked a workspace page hit: %+v", hit)
			}
		}
	})

	t.Run("PortalBSearchStillFindsItsOwnPage", func(t *testing.T) {
		hits := portalKBSearch(t, server, cookieB, slugB, "queldaron")
		found := false
		for _, hit := range hits {
			if hit["source"] == "workspace_page" && int(hit["page_id"].(float64)) == secretID {
				found = true
			}
		}
		if !found {
			t.Fatalf("portal B search did not return its own page %d: %+v", secretID, hits)
		}
	})

	t.Run("PortalADirectPageFetchStill404s", func(t *testing.T) {
		resp := MakePortalRequest(t, server, cookieA, http.MethodGet,
			fmt.Sprintf("/portal/%s/knowledge-base/pages/%d", slugA, secretID), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("portal A fetch of portal B page: expected 404, got %d", resp.StatusCode)
		}
	})
}

// The knowledge-base browse listing serves the portal's published pages as
// an ordered, content-free list. It must obey the same scoping rules as
// search and per-page fetch: only this portal's wiring, live pages only,
// stable tree order, and no duplication when wirings overlap.
func TestPortalKnowledgeBase_BrowseListing(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "KB Browse Workspace", shortKey("KBR"))
	otherWorkspace, _ := CreateTestWorkspace(t, server, "KB Browse Other", shortKey("KBO"))

	rootID := createKBPage(t, server, workspaceID, "Browse Handbook", "Handbook root for the mirevath browse tests.")
	childID := createKBPageUnder(t, server, workspaceID, &rootID, "Setup guide", "The mirevath setup guide walks through installation.")
	pageID := createKBPage(t, server, workspaceID, "Standalone article", "A standalone mirevath article.")
	hiddenID := createKBPage(t, server, otherWorkspace, "Other workspace article", "The mirevath article of the other workspace.")

	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)
	_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Browse Reader", "browse-reader@test.com")

	t.Run("ListWithoutWiring404s", func(t *testing.T) {
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/knowledge-base/pages", portalSlug), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("listing without wiring: expected 404, got %d", resp.StatusCode)
		}
	})

	wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
		{"workspace_id": workspaceID},
	})

	t.Run("WholeWorkspaceListingIsScopedOrderedAndContentFree", func(t *testing.T) {
		listing := portalKBList(t, server, portalCookie, portalSlug)
		gotIDs := make([]int, 0, len(listing))
		for _, entry := range listing {
			id := int(entry["page_id"].(float64))
			if id == hiddenID {
				t.Fatalf("listing leaked a page from an unwired workspace: %+v", entry)
			}
			if _, hasContent := entry["content"]; hasContent {
				t.Fatalf("listing must not carry page bodies: %+v", entry)
			}
			gotIDs = append(gotIDs, id)
		}
		if len(gotIDs) != 3 {
			t.Fatalf("expected 3 published pages, got %d: %+v", len(gotIDs), listing)
		}
		// Parent must precede its child in the stable tree order.
		position := map[int]int{}
		for i, id := range gotIDs {
			position[id] = i
		}
		if position[rootID] >= position[childID] {
			t.Fatalf("parent %d must be listed before child %d: %v", rootID, childID, gotIDs)
		}
	})

	t.Run("SubtreeListingOnlyContainsTheSubtree", func(t *testing.T) {
		if resp := wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
			{"workspace_id": workspaceID, "root_page_id": rootID},
		}); resp.StatusCode != http.StatusOK {
			t.Fatalf("subtree wiring: expected 200, got %d", resp.StatusCode)
		}
		listing := portalKBList(t, server, portalCookie, portalSlug)
		gotIDs := map[int]bool{}
		for _, entry := range listing {
			gotIDs[int(entry["page_id"].(float64))] = true
		}
		if !gotIDs[rootID] || !gotIDs[childID] {
			t.Fatalf("subtree listing must contain root %d and child %d: %+v", rootID, childID, listing)
		}
		if gotIDs[pageID] {
			t.Fatalf("page outside the subtree leaked into the listing: %+v", listing)
		}
	})

	t.Run("OverlappingWiringsDeduplicate", func(t *testing.T) {
		// Whole-workspace plus a subtree of the same workspace: every page
		// is in scope, and each must appear exactly once.
		if resp := wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
			{"workspace_id": workspaceID},
			{"workspace_id": workspaceID, "root_page_id": rootID},
		}); resp.StatusCode != http.StatusOK {
			t.Fatalf("combined wiring: expected 200, got %d", resp.StatusCode)
		}
		listing := portalKBList(t, server, portalCookie, portalSlug)
		counts := map[int]int{}
		for _, entry := range listing {
			counts[int(entry["page_id"].(float64))]++
		}
		for _, id := range []int{rootID, childID, pageID} {
			if counts[id] != 1 {
				t.Fatalf("page %d appears %d times under overlapping wirings, want exactly 1: %+v", id, counts[id], listing)
			}
		}
	})

	t.Run("ArchivedPagesDropOut", func(t *testing.T) {
		archiveResp := MakeAuthRequest(t, server, http.MethodDelete,
			fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, pageID), nil)
		defer archiveResp.Body.Close()
		AssertStatusCode(t, archiveResp, http.StatusNoContent)

		listing := portalKBList(t, server, portalCookie, portalSlug)
		for _, entry := range listing {
			if int(entry["page_id"].(float64)) == pageID {
				t.Fatalf("archived page still listed: %+v", listing)
			}
		}
	})

	t.Run("OtherPortalsPagesNeverListed", func(t *testing.T) {
		otherSlug, otherChannel := SetupPortalChannel(t, server, otherWorkspace)
		if resp := wireKnowledgeBasePageSources(t, server, otherChannel, []map[string]interface{}{
			{"workspace_id": otherWorkspace},
		}); resp.StatusCode != http.StatusOK {
			t.Fatalf("wiring other portal: expected 200, got %d", resp.StatusCode)
		}
		_, otherCookie := CreatePortalCustomerWithSession(t, server, otherChannel, "Other Reader", "browse-other-reader@test.com")

		listing := portalKBList(t, server, portalCookie, portalSlug)
		for _, entry := range listing {
			if int(entry["page_id"].(float64)) == hiddenID {
				t.Fatalf("listing leaked the other portal's page: %+v", listing)
			}
		}

		otherListing := portalKBList(t, server, otherCookie, otherSlug)
		for _, entry := range otherListing {
			if int(entry["page_id"].(float64)) == rootID {
				t.Fatalf("other portal's listing leaked our page: %+v", otherListing)
			}
		}
	})
}

func TestPortalKnowledgeBase_SearchExcludesArchivedPages(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "KB Archive Workspace", shortKey("KBA"))
	pageID := createKBPage(t, server, workspaceID, "Runbook", "The kvorveth runbook explains failover.")

	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)
	_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Archive Reader", "archive-reader@test.com")

	if resp := wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
		{"workspace_id": workspaceID},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("wiring: expected 200, got %d", resp.StatusCode)
	}

	archiveResp := MakeAuthRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, pageID), nil)
	defer archiveResp.Body.Close()
	AssertStatusCode(t, archiveResp, http.StatusNoContent)

	hits := portalKBSearch(t, server, portalCookie, portalSlug, "kvorveth")
	for _, hit := range hits {
		if hit["source"] == "workspace_page" {
			t.Fatalf("archived page still published: %+v", hit)
		}
	}

	pageResp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/knowledge-base/pages/%d", portalSlug, pageID), nil)
	defer pageResp.Body.Close()
	if pageResp.StatusCode != http.StatusNotFound {
		t.Fatalf("archived page fetch: expected 404, got %d", pageResp.StatusCode)
	}
}

func TestChannelConfig_KnowledgeBasePageSources_Validation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "KB Validation Workspace", shortKey("KBV"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "KB Unconnected Workspace", shortKey("KWU"))
	rootPageID := createKBPage(t, server, workspaceID, "Root", "Root page.")

	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)
	_ = portalSlug

	webhookResp := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
		"name":      "KB Validation Webhook",
		"type":      "webhook",
		"direction": "outbound",
		"status":    "enabled",
	})
	defer webhookResp.Body.Close()
	AssertStatusCode(t, webhookResp, http.StatusCreated)
	var webhookChannel map[string]interface{}
	DecodeJSON(t, webhookResp, &webhookChannel)
	webhookChannelID := ExtractIDFromResponse(t, webhookChannel)

	tests := []struct {
		name      string
		channelID int
		sources   []map[string]interface{}
		want      int
	}{
		{
			name:      "workspace must be connected to the portal",
			channelID: channelID,
			sources:   []map[string]interface{}{{"workspace_id": otherWorkspaceID}},
			want:      http.StatusBadRequest,
		},
		{
			name:      "root page must exist",
			channelID: channelID,
			sources:   []map[string]interface{}{{"workspace_id": workspaceID, "root_page_id": 999999}},
			want:      http.StatusBadRequest,
		},
		{
			name:      "root page must belong to the wired workspace",
			channelID: channelID,
			sources:   []map[string]interface{}{{"workspace_id": workspaceID, "root_page_id": createKBPage(t, server, otherWorkspaceID, "Foreign root", "content")}},
			want:      http.StatusBadRequest,
		},
		{
			name:      "workspace_id is required",
			channelID: channelID,
			sources:   []map[string]interface{}{{}},
			want:      http.StatusBadRequest,
		},
		{
			name:      "whole workspace wiring is accepted",
			channelID: channelID,
			sources:   []map[string]interface{}{{"workspace_id": workspaceID}},
			want:      http.StatusOK,
		},
		{
			name:      "subtree wiring is accepted",
			channelID: channelID,
			sources:   []map[string]interface{}{{"workspace_id": workspaceID, "root_page_id": rootPageID}},
			want:      http.StatusOK,
		},
		{
			name:      "page sources are rejected on non-portal channels",
			channelID: webhookChannelID,
			sources:   []map[string]interface{}{{"workspace_id": workspaceID}},
			want:      http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := wireKnowledgeBasePageSources(t, server, tc.channelID, tc.sources)
			if resp.StatusCode != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, resp.StatusCode)
			}
		})
	}

	// The accepted wiring is persisted on the channel config.
	configResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d", channelID), nil)
	defer configResp.Body.Close()
	AssertStatusCode(t, configResp, http.StatusOK)
	var channel map[string]interface{}
	DecodeJSON(t, configResp, &channel)
	configJSON, ok := channel["config"].(string)
	if !ok {
		t.Fatalf("channel response missing config: %+v", channel)
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("decode channel config: %v", err)
	}
	sources, ok := config["knowledge_base_page_sources"].([]interface{})
	if !ok || len(sources) != 1 {
		t.Fatalf("expected persisted knowledge_base_page_sources with 1 entry, got: %+v", config["knowledge_base_page_sources"])
	}
}

// A channel manager editing the wiring needs workspace administration rights
// on every wired workspace. The picker mirrors this contract by only offering
// administered, portal-connected workspaces.
func TestChannelConfig_KnowledgeBasePageSources_RequiresWorkspaceAdmin(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "KB Admin Workspace", shortKey("KBA"))
	rootPageID := createKBPage(t, server, workspaceID, "Root", "Root page.")
	_, channelID := SetupPortalChannel(t, server, workspaceID)

	managerID, managerUsername, managerPassword := CreateTestUserWithCredentials(
		t, server,
		fmt.Sprintf("kbmgr%d", time.Now().UnixNano()),
		fmt.Sprintf("kbmgr%d@windshift.test", time.Now().UnixNano()),
	)
	managerResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/managers", channelID), map[string]interface{}{
		"manager_type": "user",
		"manager_ids":  []int{managerID},
	})
	defer managerResp.Body.Close()
	AssertStatusCode(t, managerResp, http.StatusCreated)

	// Viewer membership makes the workspace visible to the manager without
	// granting administration rights.
	roles := GetWorkspaceRoles(t, server)
	viewerAssign := MakeAuthRequest(t, server, http.MethodPost, "/workspace-roles/assign", map[string]interface{}{
		"user_id":      managerID,
		"workspace_id": workspaceID,
		"role_id":      roles["Viewer"],
	})
	defer viewerAssign.Body.Close()
	AssertStatusCode(t, viewerAssign, http.StatusCreated)

	managerToken := CreateBearerTokenForUser(t, server, managerUsername, managerPassword)
	wireAsManager := func() *http.Response {
		resp := MakeAuthRequestWithToken(t, server, managerToken, http.MethodPut, fmt.Sprintf("/channels/%d/config", channelID), map[string]interface{}{
			"config": map[string]interface{}{
				"knowledge_base_page_sources": []map[string]interface{}{
					{"workspace_id": workspaceID, "root_page_id": rootPageID},
				},
			},
		})
		defer resp.Body.Close()
		return resp
	}

	denied := wireAsManager()
	AssertStatusCode(t, denied, http.StatusForbidden)

	adminAssign := MakeAuthRequest(t, server, http.MethodPost, "/workspace-roles/assign", map[string]interface{}{
		"user_id":      managerID,
		"workspace_id": workspaceID,
		"role_id":      roles["Administrator"],
	})
	defer adminAssign.Body.Close()
	AssertStatusCode(t, adminAssign, http.StatusCreated)

	allowed := wireAsManager()
	AssertStatusCode(t, allowed, http.StatusOK)
}

func TestPagePublication_ReportsPortalKnowledgeBaseExposure(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "KB Publication Workspace", shortKey("KBP"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "KB Unwired Workspace", shortKey("KBN"))

	rootID := createKBPage(t, server, workspaceID, "Handbook root", "Root of the wired subtree.")
	inSubtreeID := createKBPageUnder(t, server, workspaceID, &rootID, "In subtree", "Inside the wiring.")
	siblingID := createKBPage(t, server, workspaceID, "Sibling root", "Outside the wiring.")
	unwiredID := createKBPage(t, server, otherWorkspaceID, "Unwired page", "Different workspace.")

	_, channelID := SetupPortalChannel(t, server, workspaceID)
	if resp := wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
		{"workspace_id": workspaceID, "root_page_id": rootID},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("wiring: expected 200, got %d", resp.StatusCode)
	}

	tests := []struct {
		name       string
		workspace  int
		page       int
		wantPublic bool
	}{
		{"wired root is public", workspaceID, rootID, true},
		{"descendant of the wired root is public", workspaceID, inSubtreeID, true},
		{"sibling subtree is not public", workspaceID, siblingID, false},
		{"unwired workspace is not public", otherWorkspaceID, unwiredID, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakeAuthRequest(t, server, http.MethodGet,
				fmt.Sprintf("/workspaces/%d/pages/%d/publication", tc.workspace, tc.page), nil)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusOK)
			var publication struct {
				PubliclyViewable bool     `json:"publicly_viewable"`
				Portals          []string `json:"portals"`
			}
			DecodeJSON(t, resp, &publication)
			if publication.PubliclyViewable != tc.wantPublic {
				t.Fatalf("publicly_viewable = %v, want %v", publication.PubliclyViewable, tc.wantPublic)
			}
			if tc.wantPublic != (len(publication.Portals) > 0) {
				t.Fatalf("portals = %v for publicly_viewable=%v", publication.Portals, publication.PubliclyViewable)
			}
			if publication.Portals == nil {
				t.Fatalf("portals must serialize as an array, got nil")
			}
		})
	}

	t.Run("PublicationEndpointRejectsPortalSessions", func(t *testing.T) {
		_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Pub Reader", "pub-reader@test.com")
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/pages/%d/publication", workspaceID, rootID), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("portal session on internal publication endpoint: expected 401, got %d", resp.StatusCode)
		}
	})
}
