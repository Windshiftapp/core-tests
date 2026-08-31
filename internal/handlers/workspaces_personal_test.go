//go:build test

package handlers

import (
	"net/http"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// --- Pure key-generation helpers --------------------------------------------

func TestSanitizePersonalWorkspaceKeyCandidate(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Alice", "ALICE"},
		{"  Alice  ", "ALICE"},
		{"Alice Smith", "ALICE-SMIT"}, // 10-char cap drops the tail
		{"Alice-Bob", "ALICE-BOB"},
		{"Alice!!!", "ALICE"},
		{"!!!", ""},
		{"", ""},
		{"abcdefghij-klmnop", "ABCDEFGHIJ"}, // truncated at 10
	}
	for _, c := range cases {
		got := sanitizePersonalWorkspaceKeyCandidate(c.in)
		if got != c.want {
			t.Errorf("sanitizePersonalWorkspaceKeyCandidate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGeneratePersonalWorkspaceKey_FallsBackThroughCandidates(t *testing.T) {
	h := &WorkspaceHandler{}

	// Display name available — used first.
	if got := h.generatePersonalWorkspaceKey("Alice", "alice", 1); got != "ALICE" {
		t.Errorf("expected ALICE, got %q", got)
	}

	// Empty display name + username populated — falls back to username.
	if got := h.generatePersonalWorkspaceKey("", "bob123", 2); got != "BOB123" {
		t.Errorf("expected BOB123, got %q", got)
	}

	// Both blank — ultimate fallback uses the user ID.
	if got := h.generatePersonalWorkspaceKey("", "", 42); got != "USER-42" {
		t.Errorf("expected USER-42, got %q", got)
	}
}

// --- GetOrCreatePersonalWorkspace HTTP handler ------------------------------

func TestWorkspaceHandler_GetOrCreatePersonalWorkspace_CreatesOnFirstCall(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "POST", "/api/workspaces/personal", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetOrCreatePersonalWorkspace, req, nil)

	rr.AssertStatusCode(http.StatusCreated)

	var ws models.Workspace
	rr.AssertJSONResponse(&ws)

	if !ws.IsPersonal {
		t.Error("Expected IsPersonal=true on returned workspace")
	}
	if ws.OwnerID == nil || *ws.OwnerID != 1 {
		t.Errorf("Expected OwnerID=1, got %v", ws.OwnerID)
	}
	if !strings.Contains(ws.Name, "Todo List") {
		t.Errorf("Expected workspace name to contain 'Todo List', got %q", ws.Name)
	}
	// Confirm the row actually landed in the DB.
	var count int
	err := tdb.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE is_personal = TRUE AND owner_id = 1`).Scan(&count)
	if err != nil {
		t.Fatalf("count personal ws: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected exactly 1 personal workspace row, got %d", count)
	}
}

func TestWorkspaceHandler_GetOrCreatePersonalWorkspace_IdempotentOnSecondCall(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// First call creates.
	req1 := testutils.CreateJSONRequest(t, "POST", "/api/workspaces/personal", nil)
	rr1 := testutils.ExecuteAuthenticatedRequest(t, handler.GetOrCreatePersonalWorkspace, req1, nil)
	rr1.AssertStatusCode(http.StatusCreated)
	var first models.Workspace
	rr1.AssertJSONResponse(&first)

	// Second call should return the existing workspace (200, same ID).
	req2 := testutils.CreateJSONRequest(t, "POST", "/api/workspaces/personal", nil)
	rr2 := testutils.ExecuteAuthenticatedRequest(t, handler.GetOrCreatePersonalWorkspace, req2, nil)
	rr2.AssertStatusCode(http.StatusOK)
	var second models.Workspace
	rr2.AssertJSONResponse(&second)

	if first.ID != second.ID {
		t.Errorf("Expected same workspace ID on second call: first=%d second=%d", first.ID, second.ID)
	}

	// Still only one row.
	var count int
	err := tdb.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE is_personal = TRUE AND owner_id = 1`).Scan(&count)
	if err != nil {
		t.Fatalf("count personal ws: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 personal workspace after idempotent call, got %d", count)
	}
}

func TestWorkspaceHandler_GetOrCreatePersonalWorkspace_HandlesKeyCollision(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// SeedTestData already seeds a workspace with key 'TEST'. The sanitized
	// candidate for first_name='Test' is also 'TEST', so the handler will fall
	// through to 'TEST-1'. Pre-create that row too so the counter has to
	// advance to 'TEST-2'.
	_, err := tdb.Exec(`
		INSERT INTO workspaces (name, key, description, active) VALUES ('Squatter', 'TEST-1', 'x', TRUE)
	`)
	if err != nil {
		t.Fatalf("pre-create squatter workspace: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/workspaces/personal", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetOrCreatePersonalWorkspace, req, nil)

	rr.AssertStatusCode(http.StatusCreated)

	var ws models.Workspace
	rr.AssertJSONResponse(&ws)
	if ws.Key == "TEST" || ws.Key == "TEST-1" {
		t.Errorf("Expected collision-resolved key, got %q", ws.Key)
	}
	if !strings.HasPrefix(ws.Key, "TEST") {
		t.Errorf("Expected resolved key to start with TEST, got %q", ws.Key)
	}
}

func TestWorkspaceHandler_GetOrCreatePersonalWorkspace_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "POST", "/api/workspaces/personal", nil)
	// ExecuteRequest dispatches the handler with no authenticated user in the
	// context, so RequireAuth should respond with 401.
	rr := testutils.ExecuteRequest(t, handler.GetOrCreatePersonalWorkspace, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}
