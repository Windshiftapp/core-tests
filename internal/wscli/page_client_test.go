package wscli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestPageClient spins up an httptest server, returns a Client wired
// to it, and a handler-customizable closure for each test. The auth
// token is fixed because every method asserts it in the headers.
//
// Mirrors the production Client struct exactly so the same paths and
// headers exercise the same code path.
func newTestPageClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := &Client{
		baseURL:    srv.URL,
		token:      "ws_test_token",
		httpClient: srv.Client(),
	}
	return c, srv
}

// readJSONBody is a small helper for asserting POST/PUT bodies.
func readJSONBody(t *testing.T, r *http.Request, dst interface{}) {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 {
		return
	}
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode body %q: %v", string(body), err)
	}
}

func TestClient_ListPages_PathAndAuth(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotContentType, gotAccept string
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		_ = json.NewEncoder(w).Encode(PageListResponse{Items: []Page{
			{ID: 1, Title: "First", WorkspaceID: 42},
			{ID: 2, Title: "Second", WorkspaceID: 42},
		}})
	})

	pages, err := c.ListPages(42)
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method: want GET, got %s", gotMethod)
	}
	if gotPath != "/rest/api/v1/workspaces/42/pages" {
		t.Errorf("path: want /rest/api/v1/workspaces/42/pages, got %s", gotPath)
	}
	if gotAuth != "Bearer ws_test_token" {
		t.Errorf("auth: want Bearer ws_test_token, got %s", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type: want application/json, got %s", gotContentType)
	}
	if gotAccept != "application/json" {
		t.Errorf("accept: want application/json, got %s", gotAccept)
	}
	if len(pages) != 2 || pages[0].Title != "First" {
		t.Errorf("payload unwrapping: got %+v", pages)
	}
}

func TestClient_GetPage_Path(t *testing.T) {
	var gotPath, gotMethod string
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewEncoder(w).Encode(Page{ID: 7, Title: "Onboarding", WorkspaceID: 42})
	})

	page, err := c.GetPage(42, 7)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method: want GET, got %s", gotMethod)
	}
	if gotPath != "/rest/api/v1/workspaces/42/pages/7" {
		t.Errorf("path: got %s", gotPath)
	}
	if page.Title != "Onboarding" {
		t.Errorf("title: got %q", page.Title)
	}
}

func TestClient_CreatePage_Body(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody PageCreateRequest
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		readJSONBody(t, r, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Page{ID: 99, Title: gotBody.Title})
	})

	parentID := 5
	page, err := c.CreatePage(42, PageCreateRequest{Title: "New", Content: "body", ParentID: &parentID})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %s", gotMethod)
	}
	if gotPath != "/rest/api/v1/workspaces/42/pages" {
		t.Errorf("path: got %s", gotPath)
	}
	if gotBody.Title != "New" || gotBody.Content != "body" {
		t.Errorf("body title/content: got %+v", gotBody)
	}
	if gotBody.ParentID == nil || *gotBody.ParentID != 5 {
		t.Errorf("parent_id: want 5, got %+v", gotBody.ParentID)
	}
	if page.ID != 99 {
		t.Errorf("decoded id: got %d", page.ID)
	}
}

func TestClient_UpdatePage_Body(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody PageUpdateRequest
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		readJSONBody(t, r, &gotBody)
		_ = json.NewEncoder(w).Encode(Page{ID: 7})
	})

	title := "Edited"
	content := "rewritten"
	_, err := c.UpdatePage(42, 7, PageUpdateRequest{Title: &title, Content: &content})
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method: want PUT, got %s", gotMethod)
	}
	if gotPath != "/rest/api/v1/workspaces/42/pages/7" {
		t.Errorf("path: got %s", gotPath)
	}
	if gotBody.Title == nil || *gotBody.Title != "Edited" {
		t.Errorf("title: got %+v", gotBody.Title)
	}
	if gotBody.Content == nil || *gotBody.Content != "rewritten" {
		t.Errorf("content: got %+v", gotBody.Content)
	}
}

func TestClient_MovePage_RootViaNullParent(t *testing.T) {
	var gotBody PageMoveRequest
	var gotPath string
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		readJSONBody(t, r, &gotBody)
		_ = json.NewEncoder(w).Encode(Page{ID: 7, ParentID: nil})
	})

	if _, err := c.MovePage(42, 7, nil, nil, nil); err != nil {
		t.Fatalf("MovePage(root): %v", err)
	}
	if gotPath != "/rest/api/v1/workspaces/42/pages/7/move" {
		t.Errorf("path: got %s", gotPath)
	}
	if gotBody.ParentID != nil {
		t.Errorf("parent_id for root move: want nil, got %+v", gotBody.ParentID)
	}
}

func TestClient_MovePage_NonRoot(t *testing.T) {
	var gotBody PageMoveRequest
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		readJSONBody(t, r, &gotBody)
		_ = json.NewEncoder(w).Encode(Page{ID: 7})
	})

	parent := 11
	if _, err := c.MovePage(42, 7, &parent, nil, nil); err != nil {
		t.Fatalf("MovePage: %v", err)
	}
	if gotBody.ParentID == nil || *gotBody.ParentID != 11 {
		t.Errorf("parent_id: want 11, got %+v", gotBody.ParentID)
	}
}

func TestClient_MovePage_CrossWorkspace(t *testing.T) {
	var gotBody PageMoveRequest
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		readJSONBody(t, r, &gotBody)
		_ = json.NewEncoder(w).Encode(Page{ID: 7, WorkspaceID: 77})
	})

	destination := 77
	if _, err := c.MovePage(42, 7, nil, nil, nil, &destination); err != nil {
		t.Fatalf("MovePage cross-workspace: %v", err)
	}
	if gotBody.DestinationWorkspaceID == nil || *gotBody.DestinationWorkspaceID != destination {
		t.Fatalf("destination_workspace_id: got %+v, want %d", gotBody.DestinationWorkspaceID, destination)
	}
}

func TestClient_ArchivePage_Path(t *testing.T) {
	var gotMethod, gotPath string
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.ArchivePage(42, 7); err != nil {
		t.Fatalf("ArchivePage: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method: want DELETE, got %s", gotMethod)
	}
	if gotPath != "/rest/api/v1/workspaces/42/pages/7" {
		t.Errorf("path: got %s", gotPath)
	}
}

func TestClient_GetPageHistory_Path(t *testing.T) {
	var gotPath string
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(PageHistoryResponse{
			Items: []PageRevision{{ID: 1, RevisionNumber: 1}, {ID: 2, RevisionNumber: 2}},
		})
	})

	revs, err := c.GetPageHistory(42, 7)
	if err != nil {
		t.Fatalf("GetPageHistory: %v", err)
	}
	if gotPath != "/rest/api/v1/workspaces/42/pages/7/history" {
		t.Errorf("path: got %s", gotPath)
	}
	if len(revs) != 2 {
		t.Errorf("items: want 2, got %d", len(revs))
	}
}

// Error responses translate into an *APIError so callers can detect
// well-formed server errors and reach for .Code / .Message. Plain text
// 4xx/5xx fall through to the fmt.Errorf path.
func TestClient_ErrorResponseMapping(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		wantAPIErr  bool
		wantContain string
	}{
		{
			name:        "JSON error → APIError",
			status:      http.StatusForbidden,
			body:        `{"code":"INSUFFICIENT_PERMISSION","message":"missing pages:write"}`,
			wantAPIErr:  true,
			wantContain: "missing pages:write",
		},
		{
			name:        "plain-text error → generic error",
			status:      http.StatusInternalServerError,
			body:        "boom",
			wantAPIErr:  false,
			wantContain: "status 500",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestPageClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := c.ListPages(42)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantContain) {
				t.Errorf("error message: want substring %q, got %q", tc.wantContain, err.Error())
			}
			if _, ok := err.(*APIError); ok != tc.wantAPIErr {
				t.Errorf("APIError typing: want %v, got %T", tc.wantAPIErr, err)
			}
		})
	}
}

// Regression for bug-hunt finding #6: the CLI must serialize
// --before/--after as prev_sibling_id / next_sibling_id so the server
// (which already supports sibling-aware moves) gets the placement hints.
func TestClient_MovePage_SerializesSiblings(t *testing.T) {
	var gotBody PageMoveRequest
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		readJSONBody(t, r, &gotBody)
		_ = json.NewEncoder(w).Encode(Page{ID: 42})
	})

	parent := 7
	prev := 11
	if _, err := c.MovePage(99, 42, &parent, &prev, nil); err != nil {
		t.Fatalf("MovePage: %v", err)
	}
	if gotBody.ParentID == nil || *gotBody.ParentID != 7 {
		t.Errorf("parent_id: want &7, got %+v", gotBody.ParentID)
	}
	if gotBody.PrevSiblingID == nil || *gotBody.PrevSiblingID != 11 {
		t.Errorf("prev_sibling_id: want &11, got %+v", gotBody.PrevSiblingID)
	}
	if gotBody.NextSiblingID != nil {
		t.Errorf("next_sibling_id should be omitted, got %+v", gotBody.NextSiblingID)
	}

	// And the inverse: --before X populates next_sibling_id. Reset
	// gotBody first — omitempty fields decode-merge with prior state.
	gotBody = PageMoveRequest{}
	next := 9
	if _, err := c.MovePage(99, 42, &parent, nil, &next); err != nil {
		t.Fatalf("MovePage: %v", err)
	}
	if gotBody.NextSiblingID == nil || *gotBody.NextSiblingID != 9 {
		t.Errorf("next_sibling_id: want &9, got %+v", gotBody.NextSiblingID)
	}
	if gotBody.PrevSiblingID != nil {
		t.Errorf("prev_sibling_id should be omitted, got %+v", gotBody.PrevSiblingID)
	}
}

// Bare move (no siblings) — both fields must be omitted from the wire
// body so server-default placement kicks in.
func TestClient_MovePage_OmitsEmptySiblings(t *testing.T) {
	var rawBody []byte
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(Page{ID: 42})
	})
	parent := 7
	if _, err := c.MovePage(99, 42, &parent, nil, nil); err != nil {
		t.Fatalf("MovePage: %v", err)
	}
	body := string(rawBody)
	if strings.Contains(body, "prev_sibling_id") {
		t.Errorf("nil siblings should omit prev_sibling_id; body=%s", body)
	}
	if strings.Contains(body, "next_sibling_id") {
		t.Errorf("nil siblings should omit next_sibling_id; body=%s", body)
	}
}
