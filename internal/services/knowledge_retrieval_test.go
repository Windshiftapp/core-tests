package services

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
)

// Knowledge retrieval reaches across pages + page_chunks tables and exercises
// the page permission evaluator, so this test uses the full schema (via
// Initialize) rather than the minimal in-memory stub the per-service tests
// use elsewhere.
func newKnowledgeTestDB(t *testing.T) (database.Database, *PermissionService) {
	t.Helper()
	dsn := "file:knowledge-" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	cfg := DefaultPermissionCacheConfig()
	cfg.WarmupOnStartup = false
	cfg.TTL = 1 * time.Minute
	perm, err := NewPermissionService(db, cfg)
	if err != nil {
		t.Fatalf("perm service: %v", err)
	}
	t.Cleanup(func() { perm.Close() })

	if _, err := db.Exec(`INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active) VALUES (1, 'e@a', 'alice', 'A', 'A', 'h', true), (2, 'b@b', 'bob', 'B', 'B', 'h', true)`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (1, 'WS', 'WS1', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	// Force gated mode by assigning at least one user to each role in the
	// Viewer→Editor→Tester chain. Without an explicit Viewer assignment
	// the workspace stays in "Viewer-everyone" mode and any logged-in
	// user inherits page.view — masking permission bugs we want to catch.
	var viewerRoleID, editorRoleID, adminRoleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name='Viewer'`).Scan(&viewerRoleID); err != nil {
		t.Fatalf("viewer role: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name='Editor'`).Scan(&editorRoleID); err != nil {
		t.Fatalf("editor role: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name='Administrator'`).Scan(&adminRoleID); err != nil {
		t.Fatalf("admin role: %v", err)
	}
	// User 1 → Editor (and Viewer via Editor's grants), user 2 → Admin,
	// plus a phantom user 998 → Viewer to gate the workspace.
	if _, err := db.Exec(`INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active) VALUES (998, 'p@p', 'phantom', 'P', 'P', 'h', true)`); err != nil {
		t.Fatalf("seed phantom: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (1, 1, ?, CURRENT_TIMESTAMP),
		       (2, 1, ?, CURRENT_TIMESTAMP),
		       (998, 1, ?, CURRENT_TIMESTAMP)
	`, editorRoleID, adminRoleID, viewerRoleID); err != nil {
		t.Fatalf("seed roles: %v", err)
	}
	return db, perm
}

func TestKnowledgeRetrieval_ReturnsHitsForOpenPages(t *testing.T) {
	db, perm := newKnowledgeTestDB(t)
	pageSvc := NewPageService(db)
	auth := NewPagePermissionService(db, perm)
	retrieval := NewKnowledgeRetrievalService(db, auth)

	if _, err := pageSvc.Create(1, CreatePageInput{
		WorkspaceID: 1,
		Title:       "Customer Onboarding",
		Content:     "# Onboarding\n\nThis runbook describes how to provision a new client account.",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	results, err := retrieval.Search(SearchInput{UserID: 1, WorkspaceID: 1, Query: "onboarding"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one hit for 'onboarding'")
	}
	if results[0].Title != "Customer Onboarding" {
		t.Errorf("first hit title: want 'Customer Onboarding', got %q", results[0].Title)
	}
	if results[0].Source != KnowledgeSourcePage {
		t.Errorf("source: want page, got %q", results[0].Source)
	}
	if !strings.Contains(results[0].URL, "/workspaces/1/pages/") {
		t.Errorf("url: want workspaces/1/pages/..., got %q", results[0].URL)
	}
}

func TestKnowledgeRetrieval_FiltersByPagePermission(t *testing.T) {
	db, perm := newKnowledgeTestDB(t)
	pageSvc := NewPageService(db)
	auth := NewPagePermissionService(db, perm)
	retrieval := NewKnowledgeRetrievalService(db, auth)

	// Add a stranger user that has no workspace role — should see nothing.
	if _, err := db.Exec(`INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active) VALUES (3, 'c@c', 'carol', 'C', 'C', 'h', true)`); err != nil {
		t.Fatalf("seed stranger: %v", err)
	}

	page, err := pageSvc.Create(1, CreatePageInput{
		WorkspaceID: 1,
		Title:       "Secret Procedure",
		Content:     "# Secret\n\nProprietary process steps.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Editor user (id 1) sees the result.
	results, err := retrieval.Search(SearchInput{UserID: 1, WorkspaceID: 1, Query: "proprietary"})
	if err != nil {
		t.Fatalf("editor search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("editor should see the page")
	}

	// Stranger sees nothing because they lack workspace page.view.
	results, err = retrieval.Search(SearchInput{UserID: 3, WorkspaceID: 1, Query: "proprietary"})
	if err != nil {
		t.Fatalf("stranger search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("stranger should see nothing, got %d results", len(results))
	}

	_ = page // keep reference for clarity; not asserted directly
}

func TestKnowledgeRetrieval_RespectsSourceWhitelist(t *testing.T) {
	db, perm := newKnowledgeTestDB(t)
	pageSvc := NewPageService(db)
	auth := NewPagePermissionService(db, perm)
	retrieval := NewKnowledgeRetrievalService(db, auth)

	if _, err := pageSvc.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Doc", Content: "hello there"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Restricting to "logbook" should return zero hits even though a page
	// exists — Phase 1 has no logbook adapter wired.
	results, err := retrieval.Search(SearchInput{
		UserID:      1,
		WorkspaceID: 1,
		Query:       "hello",
		Sources:     []string{"logbook"},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("logbook-only search should return 0, got %d", len(results))
	}

	// Restricting to "page" still returns hits.
	results, err = retrieval.Search(SearchInput{
		UserID:      1,
		WorkspaceID: 1,
		Query:       "hello",
		Sources:     []string{"page"},
	})
	if err != nil {
		t.Fatalf("page-only search: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("page-only search should return >= 1, got 0")
	}
}

// Sanity check on the per-result chunk identity so downstream consumers
// (AI tools, UI snippet rendering) can reliably reference the chunk.
func TestKnowledgeRetrieval_PopulatesChunkIDAndPageID(t *testing.T) {
	db, perm := newKnowledgeTestDB(t)
	pageSvc := NewPageService(db)
	auth := NewPagePermissionService(db, perm)
	retrieval := NewKnowledgeRetrievalService(db, auth)

	page, err := pageSvc.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Docs", Content: "alpha bravo charlie"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	results, err := retrieval.Search(SearchInput{UserID: 1, WorkspaceID: 1, Query: "alpha"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	r := results[0]
	if r.PageID != page.ID {
		t.Errorf("page_id: want %d, got %d", page.ID, r.PageID)
	}
	if r.ChunkID == 0 {
		t.Errorf("chunk_id should be populated, got %d", r.ChunkID)
	}
	if r.URL == "" || !strings.Contains(r.URL, strconv.Itoa(page.ID)) {
		t.Errorf("url should reference page id %d, got %q", page.ID, r.URL)
	}
}
