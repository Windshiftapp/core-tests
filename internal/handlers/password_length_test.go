//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"windshift/internal/auth"
	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/middleware"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/utils"
)

// newPasswordAuthHandler builds an AuthHandler with the dependencies the
// password paths touch. The auth-policy handler is nil, which Login treats as
// "no policy" and is never reached by the rejection paths under test.
func newPasswordAuthHandler(t *testing.T, db database.Database) *AuthHandler {
	t.Helper()
	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL: time.Minute, MaxCacheSize: 1, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })

	rateLimiter := middleware.NewRateLimiter(100, 100, false, nil)
	t.Cleanup(rateLimiter.Stop)

	sessionManager := auth.NewSessionManagerWithValidationCacheTTL(
		db, false, false, nil, "password-length-test-secret", "strict", auth.DefaultSessionValidationCacheTTL,
	)
	return NewAuthHandler(
		repository.NewUserRepository(db),
		repository.NewCredentialRepository(db),
		logger.NewAuditor(db),
		sessionManager,
		rateLimiter,
		permissionService,
		nil,
		utils.NewIPExtractor(false, nil),
		nil,
		nil,
	)
}

func TestLoginRejectsOverlongPasswordInsteadOfTruncating(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "password-length-login.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	password72 := strings.Repeat("a", 72)
	hash, err := bcrypt.GenerateFromPassword([]byte(password72), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	insertAuthTestUser(t, db, "long-password@example.com", "long-password", string(hash))
	handler := newPasswordAuthHandler(t, db)

	// The exact 72-byte password authenticates.
	response, cookies := performPasswordLogin(t, handler, "long-password", password72)
	if response.Code != http.StatusOK {
		t.Fatalf("72-byte login status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if len(cookies) == 0 {
		t.Fatal("72-byte login issued no session cookie")
	}

	// A suffix past the 72-byte boundary must not match through bcrypt truncation.
	response, cookies = performPasswordLogin(t, handler, "long-password", password72+"suffix")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("over-long login status = %d, want 401; body=%s", response.Code, response.Body.String())
	}
	if len(cookies) != 0 {
		t.Fatal("over-long login issued a session cookie")
	}
}

func TestCreateUserRejectsOverlongPassword(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	invService, permService := createInvitationTestServices(t, tdb)
	handler := newTestUserHandler(tdb, permService, invService)

	req := testutils.CreateJSONRequest(t, "POST", "/api/users", map[string]any{
		"email":      "overlong-create@example.com",
		"username":   "overlong-create",
		"first_name": "Over",
		"last_name":  "Long",
		"password":   strings.Repeat("a", 73),
	})
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	testutils.AssertValidationError(t, rr, "72 bytes or fewer")

	var count int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, "overlong-create@example.com").Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Fatalf("user count = %d, want 0", count)
	}
}

func TestResetPasswordRejectsOverlongPassword(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	invService, permService := createInvitationTestServices(t, tdb)
	handler := newTestUserHandler(tdb, permService, invService)

	adminID := insertAuthTestUser(t, tdb.GetDatabase(), "reset-admin@example.com", "reset-admin", "not-a-real-hash")
	var adminPermissionID int
	if err := tdb.QueryRow(`SELECT id FROM permissions WHERE permission_key = 'system.admin'`).Scan(&adminPermissionID); err != nil {
		t.Fatalf("find system.admin permission: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO user_global_permissions (user_id, permission_id) VALUES (?, ?)`, adminID, adminPermissionID); err != nil {
		t.Fatalf("grant system.admin: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/users/999/reset-password", map[string]any{
		"password": strings.Repeat("a", 73),
	})
	req.SetPathValue("id", "999")
	admin := &models.User{ID: adminID, Email: "reset-admin@example.com", Username: "reset-admin"}
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ResetPassword, req, admin)
	testutils.AssertValidationError(t, rr, "72 bytes or fewer")
}

func TestChangePasswordRejectsOverlongPasswords(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "password-length-change.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	password72 := strings.Repeat("b", 72)
	hash, err := bcrypt.GenerateFromPassword([]byte(password72), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	userID := insertAuthTestUser(t, db, "change-password@example.com", "change-password", string(hash))
	handler := newPasswordAuthHandler(t, db)
	session := &auth.Session{UserID: userID, User: &models.User{ID: userID, Email: "change-password@example.com"}}

	// An over-long new password is a validation error, not a 500 from bcrypt.
	rr := testutils.ExecuteRequest(t, handler.ChangePassword,
		changePasswordRequest(t, session, password72, strings.Repeat("c", 73)))
	testutils.AssertValidationError(t, rr, "72 bytes or fewer")

	// A suffix past the 72-byte boundary must not match the current password.
	rr = testutils.ExecuteRequest(t, handler.ChangePassword,
		changePasswordRequest(t, session, password72+"suffix", "ValidNewPassword1!"))
	rr.AssertStatusCode(http.StatusUnauthorized)

	// The stored hash still matches only the original 72-byte password.
	var stored string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&stored); err != nil {
		t.Fatalf("read stored hash: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(password72)); err != nil {
		t.Fatalf("stored hash no longer matches the original password: %v", err)
	}
}

func changePasswordRequest(t *testing.T, session *auth.Session, current, next string) *http.Request {
	t.Helper()
	body, err := json.Marshal(ChangePasswordRequest{CurrentPassword: current, NewPassword: next})
	if err != nil {
		t.Fatalf("marshal change-password request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/auth/change-password", bytes.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), middleware.ContextKeySession, session))
}
