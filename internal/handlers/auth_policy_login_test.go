package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"windshift/internal/auth"
	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/middleware"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/utils"
)

func TestPasswordLoginCreatesOnlyRestrictedSessionsUnderPasskeyPolicies(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "auth-policy.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	password := "correct horse battery staple"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	withPasskeyID := insertAuthTestUser(t, db, "with-passkey@example.com", "with-passkey", string(passwordHash))
	insertAuthTestUser(t, db, "without-passkey@example.com", "without-passkey", string(passwordHash))
	adminID := insertAuthTestUser(t, db, "fallback-admin@example.com", "fallback-admin", string(passwordHash))
	var adminPermissionID int
	if err := db.QueryRow(`SELECT id FROM permissions WHERE permission_key = 'system.admin'`).Scan(&adminPermissionID); err != nil {
		t.Fatalf("find system.admin permission: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO user_global_permissions (user_id, permission_id) VALUES (?, ?)`, adminID, adminPermissionID); err != nil {
		t.Fatalf("grant system.admin: %v", err)
	}
	_, err = db.ExecWrite(`
		INSERT INTO webauthn_credentials (id, user_id, credential_name, public_key)
		VALUES (?, ?, ?, ?)
	`, "auth-policy-passkey", withPasskeyID, "Test passkey", []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("insert WebAuthn credential: %v", err)
	}

	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL: time.Minute, MaxCacheSize: 1, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	rateLimiter := middleware.NewRateLimiter(100, 100, false, nil)
	defer rateLimiter.Stop()
	sessionManager := auth.NewSessionManager(db, false, false, nil, "auth-policy-test-secret", "strict")
	policyHandler := NewAuthPolicyHandlerWithFallback(db, false, logger.NewAuditor(db))
	handler := NewAuthHandler(
		repository.NewUserRepository(db),
		repository.NewCredentialRepository(db),
		logger.NewAuditor(db),
		sessionManager,
		rateLimiter,
		permissionService,
		nil,
		utils.NewIPExtractor(false, nil),
		policyHandler,
		nil,
	)

	if err := policyHandler.upsertSetting("auth_policy", string(AuthPolicyPasskeyOnly), "string", "test", "auth"); err != nil {
		t.Fatalf("set passkey-only policy: %v", err)
	}
	response, cookies := performPasswordLogin(t, handler, "with-passkey", password)
	if response.Code != http.StatusForbidden {
		t.Fatalf("passkey-only password status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if len(cookies) != 0 {
		t.Fatal("passkey-only password login with an enrolled passkey issued a cookie")
	}

	response, cookies = performPasswordLogin(t, handler, "without-passkey", password)
	var enrollment LoginResponse
	decodeLoginResponse(t, response, &enrollment)
	if response.Code != http.StatusOK || !enrollment.Success || !enrollment.EnrollmentRequired {
		t.Fatalf("enrollment response = status %d, %+v", response.Code, enrollment)
	}
	assertRestrictedSessionCookie(t, sessionManager, cookies, auth.AuthPendingEnrollment)

	if err := policyHandler.upsertSetting("auth_policy", string(AuthPolicyPasswordPasskey2FA), "string", "test", "auth"); err != nil {
		t.Fatalf("set password+passkey policy: %v", err)
	}
	response, cookies = performPasswordLogin(t, handler, "with-passkey", password)
	var pending LoginResponse
	decodeLoginResponse(t, response, &pending)
	if response.Code != http.StatusOK || pending.Success || !pending.PasskeyRequired {
		t.Fatalf("password+passkey response = status %d, %+v", response.Code, pending)
	}
	assertRestrictedSessionCookie(t, sessionManager, cookies, auth.AuthPendingPasskeyVerification)

	if err := policyHandler.upsertSetting("auth_policy_preview", "true", "boolean", "test", "auth"); err != nil {
		t.Fatalf("enable preview mode: %v", err)
	}
	response, cookies = performPasswordLogin(t, handler, "with-passkey", password)
	var preview LoginResponse
	decodeLoginResponse(t, response, &preview)
	if response.Code != http.StatusOK || !preview.Success || preview.PasskeyRequired || preview.EnrollmentRequired {
		t.Fatalf("preview response = status %d, %+v", response.Code, preview)
	}
	assertNormalSessionCookie(t, sessionManager, cookies)

	// Explicit administrator fallback is the sole password-only exception.
	if err := policyHandler.upsertSetting("auth_policy_preview", "false", "boolean", "test", "auth"); err != nil {
		t.Fatalf("disable preview mode: %v", err)
	}
	if err := policyHandler.upsertSetting("auth_policy", string(AuthPolicyPasskeyOnly), "string", "test", "auth"); err != nil {
		t.Fatalf("restore passkey-only policy: %v", err)
	}
	fallbackPolicy := NewAuthPolicyHandlerWithFallback(db, true, logger.NewAuditor(db))
	fallbackHandler := NewAuthHandler(
		repository.NewUserRepository(db),
		repository.NewCredentialRepository(db),
		logger.NewAuditor(db),
		sessionManager,
		rateLimiter,
		permissionService,
		nil,
		utils.NewIPExtractor(false, nil),
		fallbackPolicy,
		middleware.NewAdminFallbackRateLimiter(db),
	)
	response, cookies = performPasswordLogin(t, fallbackHandler, "fallback-admin", password)
	var fallback LoginResponse
	decodeLoginResponse(t, response, &fallback)
	if response.Code != http.StatusOK || !fallback.Success || fallback.PasskeyRequired || fallback.EnrollmentRequired {
		t.Fatalf("admin fallback response = status %d, %+v", response.Code, fallback)
	}
	assertNormalSessionCookie(t, sessionManager, cookies)
}

func insertAuthTestUser(t *testing.T, db database.Database, email, username, passwordHash string) int {
	t.Helper()
	result, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active, email_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, true, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, email, username, "Auth", "Test", passwordHash)
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func performPasswordLogin(t *testing.T, handler *AuthHandler, username, password string) (*httptest.ResponseRecorder, []*http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(LoginRequest{EmailOrUsername: username, Password: password})
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	request.RemoteAddr = "198.51.100.30:4567"
	response := httptest.NewRecorder()
	handler.Login(response, request)
	return response, response.Result().Cookies()
}

func decodeLoginResponse(t *testing.T, response *httptest.ResponseRecorder, target *LoginResponse) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode login response %q: %v", response.Body.String(), err)
	}
}

func assertRestrictedSessionCookie(t *testing.T, manager *auth.SessionManager, cookies []*http.Cookie, expectedType string) {
	t.Helper()
	session := sessionFromCookies(t, manager, cookies)
	if !session.EnrollmentRequired || session.AuthPendingType != expectedType {
		t.Fatalf("pending session state = required %v, type %q; want required true, type %q", session.EnrollmentRequired, session.AuthPendingType, expectedType)
	}
}

func assertNormalSessionCookie(t *testing.T, manager *auth.SessionManager, cookies []*http.Cookie) {
	t.Helper()
	session := sessionFromCookies(t, manager, cookies)
	if session.EnrollmentRequired {
		t.Fatal("preview mode issued a restricted session")
	}
}

func sessionFromCookies(t *testing.T, manager *auth.SessionManager, cookies []*http.Cookie) *auth.Session {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	token, err := manager.GetSessionFromCookie(request)
	if err != nil {
		t.Fatalf("GetSessionFromCookie: %v", err)
	}
	session, err := manager.ValidateSession(token, "198.51.100.30")
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	return session
}
