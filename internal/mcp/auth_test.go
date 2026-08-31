package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/database"
	"windshift/internal/models"
)

func TestBearerAuthMiddleware_DeleteRequiresAuth(t *testing.T) {
	tm, _, _ := newMCPTestEnv(t)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	mw := bearerAuthMiddleware(tm, next)

	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("Mcp-Session-Id", "any")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("DELETE without Authorization: got status %d, want 401 (body: %q)", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatalf("DELETE without Authorization reached the wrapped handler — the bypass is back")
	}
}

func TestBearerAuthMiddleware_DeleteWithValidTokenPasses(t *testing.T) {
	tm, _, userID := newMCPTestEnv(t)

	resp, err := tm.CreateToken(userID, models.APITokenCreate{
		Name:        "mcp-test",
		Permissions: []string{auth.ScopeMCPAccess},
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	mw := bearerAuthMiddleware(tm, next)

	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+resp.Token)
	req.Header.Set("Mcp-Session-Id", "any")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE with valid token: got status %d, want 204 (body: %q)", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatalf("DELETE with valid token did not reach the wrapped handler")
	}
}

func TestBearerAuthMiddleware_DeleteRejectsTokenWithoutScope(t *testing.T) {
	tm, _, userID := newMCPTestEnv(t)

	resp, err := tm.CreateToken(userID, models.APITokenCreate{
		Name:        "no-mcp",
		Permissions: []string{"items:read"}, // deliberately omits mcp:access
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached when scope check fails")
	})

	mw := bearerAuthMiddleware(tm, next)

	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+resp.Token)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE with mismatched scope: got status %d, want 403", rec.Code)
	}
}

func newMCPTestEnv(t *testing.T) (*auth.TokenManager, database.Database, int) {
	t.Helper()

	dsn := fmt.Sprintf("file:mcpauth-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	res, err := db.Exec(`INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, ?, '')`,
		"mcp@example.com", "mcp", "Mcp")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	uid64, _ := res.LastInsertId()
	tm := auth.NewTokenManager(db, nil)
	return tm, db, int(uid64)
}
