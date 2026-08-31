package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/restapi"
	"windshift/internal/services"
)

// newSearchTestDB returns a freshly-initialized SQLite database for the v1
// page-search integration tests. Mirrors the cookie-handler test harness
// (newNegativeTestDB) but local to this package.
func newSearchTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return db
}

func newSearchPermService(t *testing.T, db database.Database) *services.PermissionService {
	t.Helper()
	cfg := services.DefaultPermissionCacheConfig()
	cfg.WarmupOnStartup = false
	cfg.TTL = time.Minute
	ps, err := services.NewPermissionService(db, cfg)
	if err != nil {
		t.Fatalf("perm service: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps
}

func seedSearchUser(t *testing.T, db database.Database, id int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (?, ?, ?, 'T', 'U', '$2a$10$hash', TRUE) ON CONFLICT DO NOTHING`,
		id, fmt.Sprintf("u%d@example.com", id), fmt.Sprintf("searchuser%d", id)); err != nil {
		t.Fatalf("seed user %d: %v", id, err)
	}
}

// seedSearchWorkspaceRole inserts a workspace (if absent) and grants userID
// the named role. Any role grant flips the workspace into gated mode (memory:
// workspaces are open by default), so visibility is governed by the grant.
func seedSearchWorkspaceRole(t *testing.T, db database.Database, wsID, userID int, role string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (?, 'WS', ?, true) ON CONFLICT (id) DO NOTHING`,
		wsID, fmt.Sprintf("WS%d", wsID)); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	var roleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, role).Scan(&roleID); err != nil {
		t.Fatalf("look up role %s: %v", role, err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, userID, wsID, roleID); err != nil {
		t.Fatalf("assign role: %v", err)
	}
}

func searchRequest(target string, userID int, pathVals map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := context.WithValue(req.Context(), restapi.ContextKeyUser, &models.User{ID: userID})
	req = req.WithContext(ctx)
	for k, v := range pathVals {
		req.SetPathValue(k, v)
	}
	return req
}

// TestV1PageHandler_Search_MatchesTitleAndBodyButOmitsBody verifies keyword
// discovery covers page content without returning that content in the result.
func TestV1PageHandler_Search_MatchesTitleAndBodyButOmitsBody(t *testing.T) {
	db := newSearchTestDB(t)
	perm := newSearchPermService(t, db)
	h := NewPageHandler(db, perm)
	const userID = 1
	seedSearchUser(t, db, userID)
	seedSearchWorkspaceRole(t, db, 1, userID, "Editor")

	svc := services.NewPageService(db)
	if _, err := svc.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Deployment Runbook", Content: "secret body text"}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if _, err := svc.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Onboarding Guide"}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if _, err := svc.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Operations", Content: "The runbook keyword appears in this body."}); err != nil {
		t.Fatalf("create body-match page: %v", err)
	}

	req := searchRequest("/workspaces/1/pages/search?q=runbook", userID, map[string]string{"id": "1"})
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("want 2 matches, got %d: %s", len(resp.Items), rr.Body.String())
	}
	if resp.Items[0].Title != "Deployment Runbook" {
		t.Errorf("title: got %q, want %q", resp.Items[0].Title, "Deployment Runbook")
	}
	for _, item := range resp.Items {
		if item.Content != "" {
			t.Errorf("search must omit body for %q, got content %q", item.Title, item.Content)
		}
	}
}

// TestV1PageHandler_Search_RespectsWorkspaceBoundary confirms a search in one
// workspace never surfaces a page from another.
func TestV1PageHandler_Search_RespectsWorkspaceBoundary(t *testing.T) {
	db := newSearchTestDB(t)
	perm := newSearchPermService(t, db)
	h := NewPageHandler(db, perm)
	const userID = 1
	seedSearchUser(t, db, userID)
	seedSearchUser(t, db, 999)
	seedSearchWorkspaceRole(t, db, 1, userID, "Editor")
	seedSearchWorkspaceRole(t, db, 2, 999, "Administrator")

	svc := services.NewPageService(db)
	if _, err := svc.Create(999, services.CreatePageInput{WorkspaceID: 2, Title: "Runbook Secret"}); err != nil {
		t.Fatalf("create page: %v", err)
	}

	req := searchRequest("/workspaces/1/pages/search?q=runbook", userID, map[string]string{"id": "1"})
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("workspace boundary leak: want 0 results, got %d", len(resp.Items))
	}
}

// TestParseSearchLimit pins the page-search limit semantics (default 20, hard
// cap 100; non-positive / unparseable falls back to the default).
func TestParseSearchLimit(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 20},
		{"?limit=5", 5},
		{"?limit=0", 20},
		{"?limit=-3", 20},
		{"?limit=abc", 20},
		{"?limit=100", 100},
		{"?limit=500", 100},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/x"+tc.raw, nil)
		if got := parseSearchLimit(req); got != tc.want {
			t.Errorf("parseSearchLimit(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
