//go:build test

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"windshift/internal/database"
	"windshift/internal/logger"
)

// GetAffectedUsers must return a bounded page (with total and cursor) instead
// of materializing every affected user in one unbounded response.
func TestGetAffectedUsersPaginatesLargeUserSet(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "auth-policy-affected.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	// The affected set is every active user without a passkey (admins are
	// included but flagged), matching the current contract.
	const affectedCount = 60
	const adminCount = 2
	for i := 0; i < affectedCount; i++ {
		insertAuthTestUser(t, db, fmt.Sprintf("affected-%03d@example.com", i), fmt.Sprintf("affected-%03d", i), string(passwordHash))
	}
	withPasskey := insertAuthTestUser(t, db, "has-passkey@example.com", "has-passkey", string(passwordHash))
	if _, err := db.ExecWrite(`
		INSERT INTO webauthn_credentials (id, user_id, credential_name, public_key)
		VALUES ('affected-passkey', ?, 'Test passkey', ?)
	`, withPasskey, []byte{1, 2, 3}); err != nil {
		t.Fatalf("insert passkey: %v", err)
	}
	var adminPermissionID int
	if err := db.QueryRow(`SELECT id FROM permissions WHERE permission_key = 'system.admin'`).Scan(&adminPermissionID); err != nil {
		t.Fatalf("find system.admin permission: %v", err)
	}
	adminEmails := map[string]bool{}
	for i := 0; i < adminCount; i++ {
		email := fmt.Sprintf("fallback-admin-%d@example.com", i)
		adminID := insertAuthTestUser(t, db, email, fmt.Sprintf("fallback-admin-%d", i), string(passwordHash))
		if _, err := db.ExecWrite(`INSERT INTO user_global_permissions (user_id, permission_id) VALUES (?, ?)`, adminID, adminPermissionID); err != nil {
			t.Fatalf("grant system.admin: %v", err)
		}
		adminEmails[email] = true
	}

	handler := NewAuthPolicyHandlerWithFallback(db, false, logger.NewAuditor(db))
	if err := handler.upsertSetting("auth_policy", string(AuthPolicyPasskeyOnly), "string", "test", "auth"); err != nil {
		t.Fatalf("set passkey-only policy: %v", err)
	}

	type affectedPage struct {
		Users []struct {
			ID      int    `json:"id"`
			Email   string `json:"email"`
			IsAdmin bool   `json:"is_admin"`
		} `json:"users"`
		TotalCount int    `json:"total_count"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}

	fetchPage := func(cursor string) affectedPage {
		t.Helper()
		url := "/api/admin/auth-policy/affected?limit=10"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		request := httptest.NewRequest(http.MethodGet, url, nil)
		response := httptest.NewRecorder()
		handler.GetAffectedUsers(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("affected users status = %d, want 200; body=%s", response.Code, response.Body.String())
		}
		var page affectedPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode affected page %q: %v", response.Body.String(), err)
		}
		return page
	}

	seen := map[string]bool{}
	var first affectedPage
	cursor := ""
	for page := 0; ; page++ {
		current := fetchPage(cursor)
		if len(current.Users) > 10 {
			t.Fatalf("page %d returned %d users, want at most the limit of 10", page, len(current.Users))
		}
		for _, user := range current.Users {
			if seen[user.Email] {
				t.Fatalf("user %s returned on two pages", user.Email)
			}
			seen[user.Email] = true
		}
		if page == 0 {
			first = current
		}
		if !current.HasMore {
			break
		}
		if current.NextCursor == "" {
			t.Fatal("has_more is true but next_cursor is empty")
		}
		cursor = current.NextCursor
		if page > 20 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(seen) != affectedCount+adminCount {
		t.Fatalf("paged through %d affected users, want %d", len(seen), affectedCount+adminCount)
	}
	if seen["has-passkey@example.com"] {
		t.Fatal("user with a passkey must not be listed as affected")
	}
	for email := range adminEmails {
		if !seen[email] {
			t.Fatalf("admin %s must stay listed with the is_admin flag", email)
		}
	}
	if first.TotalCount != affectedCount+adminCount {
		t.Fatalf("total_count = %d, want %d", first.TotalCount, affectedCount+adminCount)
	}
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page has_more=%v next_cursor=%q, want more pages", first.HasMore, first.NextCursor)
	}
}
