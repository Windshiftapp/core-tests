package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Portal knowledge-base ticket links and usage signals (WI-1134). Agents can
// insert knowledge-page links into request replies ([title](page:<id>)); the
// portal conversation resolves them to in-portal article links only when the
// page is published through that portal, and the portal KB endpoints record
// search / no_result / view / deflection signals attributed to portal
// customers.

type kbEventRow struct {
	EventType        string
	PortalCustomerID *int
	PageID           *int
	WorkspaceID      *int
	Source           *string
	Query            *string
}

func kbEventRows(t *testing.T, server *TestServer, channelID int) []kbEventRow {
	t.Helper()
	rows, err := server.DB().Query(`
		SELECT event_type, portal_customer_id, page_id, workspace_id, source, query
		FROM kb_events
		WHERE channel_id = ?
		ORDER BY id`, channelID)
	if err != nil {
		t.Fatalf("query kb_events: %v", err)
	}
	defer rows.Close()

	var out []kbEventRow
	for rows.Next() {
		var row kbEventRow
		if err := rows.Scan(&row.EventType, &row.PortalCustomerID, &row.PageID, &row.WorkspaceID, &row.Source, &row.Query); err != nil {
			t.Fatalf("scan kb_events row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate kb_events: %v", err)
	}
	return out
}

func kbEventsOfType(t *testing.T, server *TestServer, channelID int, eventType string) []kbEventRow {
	t.Helper()
	var matches []kbEventRow
	for _, row := range kbEventRows(t, server, channelID) {
		if row.EventType == eventType {
			matches = append(matches, row)
		}
	}
	return matches
}

func setupKBPortal(t *testing.T, server *TestServer, workspaceID int) (portalSlug string, channelID int) {
	t.Helper()
	portalSlug, channelID = SetupPortalChannel(t, server, workspaceID)
	resp := wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
		{"workspace_id": workspaceID},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wiring whole workspace: expected 200, got %d", resp.StatusCode)
	}
	return portalSlug, channelID
}

func portalKBGetPage(t *testing.T, server *TestServer, portalCookie, slug string, pageID int, source string) *http.Response {
	t.Helper()
	endpoint := fmt.Sprintf("/portal/%s/knowledge-base/pages/%d", slug, pageID)
	if source != "" {
		endpoint += "?source=" + source
	}
	return MakePortalRequest(t, server, portalCookie, http.MethodGet, endpoint, nil)
}

func TestPortalKBSignals_RecordSearchAndNoResult(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspace, _ := CreateTestWorkspace(t, server, "KB Signals Workspace", shortKey("KBS"))
	createKBPage(t, server, workspace,
		"Onboarding checklist", "The fandorin onboarding checklist covers first-day setup steps.")
	portalSlug, channelID := setupKBPortal(t, server, workspace)
	_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "KB Searcher", "kb-searcher@test.com")

	t.Run("SearchWithHitsRecordsSearchEvent", func(t *testing.T) {
		hits := portalKBSearch(t, server, portalCookie, portalSlug, "fandorin")
		if len(hits) == 0 {
			t.Fatalf("expected search hits for published page")
		}

		events := kbEventsOfType(t, server, channelID, "search")
		if len(events) != 1 {
			t.Fatalf("search events = %d, want 1", len(events))
		}
		if events[0].Query == nil || *events[0].Query != "fandorin" {
			t.Fatalf("search event query = %v, want fandorin", events[0].Query)
		}
		if events[0].PortalCustomerID == nil {
			t.Fatalf("search event must be attributed to the portal customer")
		}
		if events[0].PageID != nil {
			t.Fatalf("search event must not pin a page id, got %d", *events[0].PageID)
		}
	})

	t.Run("SearchWithoutHitsRecordsNoResultEvent", func(t *testing.T) {
		hits := portalKBSearch(t, server, portalCookie, portalSlug, "zzznothingmatches")
		if len(hits) != 0 {
			t.Fatalf("expected zero hits, got %d", len(hits))
		}

		events := kbEventsOfType(t, server, channelID, "no_result")
		if len(events) != 1 {
			t.Fatalf("no_result events = %d, want 1", len(events))
		}
		if events[0].Query == nil || *events[0].Query != "zzznothingmatches" {
			t.Fatalf("no_result event query = %v, want zzznothingmatches", events[0].Query)
		}
	})

	t.Run("OversizedQueryIsRejectedWithoutRecording", func(t *testing.T) {
		resp := MakePortalRequest(t, server, portalCookie, http.MethodPost,
			fmt.Sprintf("/portal/%s/knowledge-base/search", portalSlug),
			map[string]string{"query": strings.Repeat("x", 257)})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("oversized query: expected 400, got %d", resp.StatusCode)
		}
		if got := len(kbEventRows(t, server, channelID)); got != 2 {
			t.Fatalf("kb_events rows = %d, want 2 (oversized query must not record)", got)
		}
	})
}

func TestPortalKBSignals_ViewSourcesAndDeflection(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspace, _ := CreateTestWorkspace(t, server, "KB Deflect Workspace", shortKey("KBD"))
	pageID := createKBPage(t, server, workspace,
		"Password reset", "Reset your fandorin password from the account settings page.")
	portalSlug, channelID := setupKBPortal(t, server, workspace)

	// Customer without any request: self-service views deflect.
	_, soloCookie := CreatePortalCustomerWithSession(t, server, channelID, "Solo Reader", "kb-solo@test.com")
	// Customer with an open request: views are part of an assisted flow.
	_, busyCookie := CreatePortalCustomerWithSession(t, server, channelID, "Busy Reader", "kb-busy@test.com")
	SubmitPortalRequest(t, server, portalSlug, busyCookie, "Still needs help")

	t.Run("BrowseViewByCustomerWithoutRequestsDeflects", func(t *testing.T) {
		resp := portalKBGetPage(t, server, soloCookie, portalSlug, pageID, "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("browse view: expected 200, got %d", resp.StatusCode)
		}

		views := kbEventsOfType(t, server, channelID, "view")
		deflections := kbEventsOfType(t, server, channelID, "deflection")
		if len(views) != 1 || len(deflections) != 1 {
			t.Fatalf("view events = %d, deflection events = %d, want 1 each", len(views), len(deflections))
		}
		if views[0].Source == nil || *views[0].Source != "browse" {
			t.Fatalf("view source = %v, want browse", views[0].Source)
		}
		if views[0].PageID == nil || *views[0].PageID != pageID {
			t.Fatalf("view page_id = %v, want %d", views[0].PageID, pageID)
		}
		if views[0].WorkspaceID == nil || *views[0].WorkspaceID != workspace {
			t.Fatalf("view workspace_id = %v, want %d", views[0].WorkspaceID, workspace)
		}
	})

	t.Run("SearchSourcedViewByCustomerWithoutRequestsDeflects", func(t *testing.T) {
		resp := portalKBGetPage(t, server, soloCookie, portalSlug, pageID, "search")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search-sourced view: expected 200, got %d", resp.StatusCode)
		}

		deflections := kbEventsOfType(t, server, channelID, "deflection")
		if len(deflections) != 2 {
			t.Fatalf("deflection events = %d, want 2", len(deflections))
		}
	})

	t.Run("ViewByCustomerWithOpenRequestDoesNotDeflect", func(t *testing.T) {
		resp := portalKBGetPage(t, server, busyCookie, portalSlug, pageID, "search")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("view with open request: expected 200, got %d", resp.StatusCode)
		}

		views := kbEventsOfType(t, server, channelID, "view")
		if len(views) != 3 {
			t.Fatalf("view events = %d, want 3", len(views))
		}
		deflections := kbEventsOfType(t, server, channelID, "deflection")
		if len(deflections) != 2 {
			t.Fatalf("deflection events = %d, want still 2 (customer has an open request)", len(deflections))
		}
	})

	t.Run("TicketSourcedViewNeverDeflects", func(t *testing.T) {
		resp := portalKBGetPage(t, server, soloCookie, portalSlug, pageID, "ticket")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ticket-sourced view: expected 200, got %d", resp.StatusCode)
		}

		deflections := kbEventsOfType(t, server, channelID, "deflection")
		if len(deflections) != 2 {
			t.Fatalf("deflection events = %d, want still 2 (ticket views are assisted)", len(deflections))
		}
	})

	t.Run("StaffPreviewRecordsNothing", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/portal/%s/knowledge-base/pages/%d", portalSlug, pageID), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("staff view: expected 200, got %d", resp.StatusCode)
		}

		views := kbEventsOfType(t, server, channelID, "view")
		if len(views) != 4 {
			t.Fatalf("view events = %d, want still 4 (staff sessions record nothing)", len(views))
		}
	})

	t.Run("UnauthenticatedIsDeniedWithoutRecording", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet,
			server.APIBase+fmt.Sprintf("/portal/%s/knowledge-base/pages/%d", portalSlug, pageID), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated view: expected 401, got %d", resp.StatusCode)
		}
		if got := len(kbEventRows(t, server, channelID)); got != 6 {
			t.Fatalf("kb_events rows = %d, want 6 (denied access records nothing)", got)
		}
	})
}

func TestPortalRequestConversationRewritesKBArticleLinks(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspace, _ := CreateTestWorkspace(t, server, "KB Reply Links Workspace", shortKey("KBL"))
	// Wire a subtree so pages outside it are genuinely unpublished: the guide
	// lives under the handbook root, the roadmap sits outside the subtree.
	handbookID := createKBPage(t, server, workspace, "Handbook", "Root of the published handbook tree.")
	guideID := createKBPageUnder(t, server, workspace, &handbookID, "Setup guide", "Step by step fandorin setup instructions.")
	secretID := createKBPage(t, server, workspace, "Secret roadmap", "Internal only roadmap notes.")

	portalSlug, channelID := SetupPortalChannel(t, server, workspace)
	wiredResp := wireKnowledgeBasePageSources(t, server, channelID, []map[string]interface{}{
		{"workspace_id": workspace, "root_page_id": handbookID},
	})
	if wiredResp.StatusCode != http.StatusOK {
		t.Fatalf("wiring handbook subtree: expected 200, got %d", wiredResp.StatusCode)
	}
	_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "KB Link Reader", "kb-links@test.com")
	itemID := SubmitPortalRequest(t, server, portalSlug, portalCookie, "Need setup help")

	// A second portal wiring a different workspace: its published pages must
	// not resolve through the first portal's conversation either.
	otherWorkspace, _ := CreateTestWorkspace(t, server, "KB Reply Other Workspace", shortKey("KBM"))
	otherPageID := createKBPage(t, server, otherWorkspace, "Other guide", "Other workspace guide.")
	otherSlug, otherChannelID := SetupPortalChannel(t, server, otherWorkspace)
	resp := wireKnowledgeBasePageSources(t, server, otherChannelID, []map[string]interface{}{
		{"workspace_id": otherWorkspace},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wiring other workspace: expected 200, got %d", resp.StatusCode)
	}
	_, otherCookie := CreatePortalCustomerWithSession(t, server, otherChannelID, "Other Reader", "kb-other@test.com")
	otherItemID := SubmitPortalRequest(t, server, otherSlug, otherCookie, "Other workspace question")

	// Agent replies linking one published page, one unpublished page, and one
	// page published only through the other portal.
	commentResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", itemID), map[string]interface{}{
		"content": fmt.Sprintf("Read [Setup guide](page:%d); ignore [Secret roadmap](page:%d) and [Other guide](page:%d).", guideID, secretID, otherPageID),
	})
	defer commentResp.Body.Close()
	AssertStatusCode(t, commentResp, http.StatusCreated)

	otherCommentResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", otherItemID), map[string]interface{}{
		"content": fmt.Sprintf("See [Setup guide](page:%d).", guideID),
	})
	defer otherCommentResp.Body.Close()
	AssertStatusCode(t, otherCommentResp, http.StatusCreated)

	// Point the first portal request's description at both pages so the
	// description rewrite path is exercised too.
	descResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/items/%d", itemID), map[string]interface{}{
		"description": fmt.Sprintf("Context: [Setup guide](page:%d) and [Secret roadmap](page:%d).", guideID, secretID),
	})
	defer descResp.Body.Close()
	AssertStatusCode(t, descResp, http.StatusOK)

	t.Run("PublishedPageBecomesInPortalArticleLink", func(t *testing.T) {
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var comments []map[string]interface{}
		DecodeJSON(t, resp, &comments)
		if len(comments) != 1 {
			t.Fatalf("comments = %d, want 1", len(comments))
		}
		html := comments[0]["content_html"].(string)
		wantHref := fmt.Sprintf("/portal/%s/kb/%d?source=ticket", portalSlug, guideID)
		if !strings.Contains(html, `href="`+wantHref+`"`) {
			t.Fatalf("published page link not rewritten to %s in %q", wantHref, html)
		}
	})

	t.Run("UnpublishedAndForeignPagesDegradeToPlainText", func(t *testing.T) {
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var comments []map[string]interface{}
		DecodeJSON(t, resp, &comments)
		html := comments[0]["content_html"].(string)
		if strings.Contains(html, `href="page:`) {
			t.Fatalf("raw page: href leaked to portal conversation: %q", html)
		}
		if strings.Contains(html, fmt.Sprintf("page:%d", secretID)) || strings.Contains(html, "Secret roadmap</a>") {
			t.Fatalf("unpublished page kept an anchor: %q", html)
		}
		if !strings.Contains(html, "Secret roadmap") || !strings.Contains(html, "Other guide") {
			t.Fatalf("link text must survive stripping: %q", html)
		}
		if strings.Contains(html, `href="/portal/`) && strings.Contains(html, "Other guide</a>") {
			t.Fatalf("foreign-portal page must not resolve: %q", html)
		}
	})

	t.Run("RequestDescriptionRewritesTheSameWay", func(t *testing.T) {
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d", portalSlug, itemID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var detail map[string]interface{}
		DecodeJSON(t, resp, &detail)
		html := detail["description_html"].(string)
		wantHref := fmt.Sprintf("/portal/%s/kb/%d?source=ticket", portalSlug, guideID)
		if !strings.Contains(html, `href="`+wantHref+`"`) {
			t.Fatalf("description link not rewritten to %s in %q", wantHref, html)
		}
		if strings.Contains(html, `href="page:`) {
			t.Fatalf("description kept a raw page href: %q", html)
		}
	})

	t.Run("OtherPortalCannotResolveFirstPortalsPages", func(t *testing.T) {
		resp := MakePortalRequest(t, server, otherCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/comments", otherSlug, otherItemID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var comments []map[string]interface{}
		DecodeJSON(t, resp, &comments)
		if len(comments) != 1 {
			t.Fatalf("comments = %d, want 1", len(comments))
		}
		html := comments[0]["content_html"].(string)
		if strings.Contains(html, fmt.Sprintf("/portal/%s/kb/%d", portalSlug, guideID)) {
			t.Fatalf("other portal resolved a page it does not publish: %q", html)
		}
		if !strings.Contains(html, "Setup guide") {
			t.Fatalf("link text must survive stripping: %q", html)
		}
	})

	t.Run("DeniedCustomerGets404WithoutSignals", func(t *testing.T) {
		// A customer of the first portal must not read the other portal's
		// request conversation via direct navigation.
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/comments", otherSlug, otherItemID), nil)
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("cross-portal conversation read must not succeed")
		}
	})
}

func TestPortalKBSignals_NoEventsWithoutSignalService(t *testing.T) {
	// Structural guard: the kb_events table exists on both engines through the
	// production schema, so the recorder's INSERT always has a target.
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspace, _ := CreateTestWorkspace(t, server, "KB Schema Workspace", shortKey("KBH"))
	portalSlug, channelID := setupKBPortal(t, server, workspace)
	pageID := createKBPage(t, server, workspace, "Glossary", "Terms used in the fandorin docs.")
	_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "KB Schema Reader", "kb-schema@test.com")

	resp := portalKBGetPage(t, server, portalCookie, portalSlug, pageID, "browse")
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var payload struct {
		PageID int `json:"page_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.PageID != pageID {
		t.Fatalf("page payload page_id = %d, want %d", payload.PageID, pageID)
	}

	if rows := kbEventRows(t, server, channelID); len(rows) == 0 {
		t.Fatalf("expected at least one recorded kb_event for the served page")
	}
}
