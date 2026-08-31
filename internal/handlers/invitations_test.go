//go:build test

package handlers

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"windshift/internal/logger"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"

	"golang.org/x/crypto/bcrypt"
)

// newTestUserHandler builds a UserHandler with the constructor's full
// dependency surface. The offboard/deactivate closures are no-ops because
// the invitation tests never exercise those paths.
func newTestUserHandler(tdb *testutils.TestDB, permService *services.PermissionService, invService *services.InvitationService) *UserHandler {
	db := tdb.GetDatabase()
	return NewUserHandler(
		repository.NewUserRepository(db),
		logger.NewAuditor(db),
		permService,
		invService,
		services.NewUserReadService(db),
		func(int) error { return nil },
		func(int) (services.AgentDeactivationResult, error) {
			return services.AgentDeactivationResult{}, nil
		},
		nil,
	)
}

// createInvitationTestServices creates the services needed for invitation handler tests.
func createInvitationTestServices(t *testing.T, tdb *testutils.TestDB) (*services.InvitationService, *services.PermissionService) {
	t.Helper()

	invService := services.NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")

	permConfig := services.DefaultPermissionCacheConfig()
	permConfig.WarmupOnStartup = false
	permConfig.TTL = 1 * time.Minute
	permService, err := services.NewPermissionService(tdb.GetDatabase(), permConfig)
	if err != nil {
		t.Fatalf("Failed to create permission service: %v", err)
	}
	t.Cleanup(func() { permService.Close() })

	return invService, permService
}

func TestInvitationHandler_FullFlow(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	invService, permService := createInvitationTestServices(t, tdb)
	userHandler := newTestUserHandler(tdb, permService, invService)
	invHandler := NewInvitationHandler(invService)

	// Step 1: Invite a new user (as admin)
	inviteReq := testutils.CreateJSONRequest(t, "POST", "/api/users/invite", map[string]interface{}{
		"email":      "invited@example.com",
		"username":   "inviteduser",
		"first_name": "Invited",
		"last_name":  "User",
		"is_active":  true,
	})
	rr := testutils.ExecuteAuthenticatedRequest(t, userHandler.InviteUser, inviteReq, nil)
	rr.AssertStatusCode(http.StatusCreated)

	// Extract token from response
	token, ok := rr.GetJSONField("token").(string)
	if !ok || token == "" {
		t.Fatal("Expected non-empty token in invite response")
	}

	// Verify invited user starts inactive
	var isActiveAfterInvite bool
	err := tdb.QueryRow(`SELECT is_active FROM users WHERE email = 'invited@example.com'`).Scan(&isActiveAfterInvite)
	if err != nil {
		t.Fatalf("Failed to query is_active after invite: %v", err)
	}
	if isActiveAfterInvite {
		t.Fatal("Expected is_active to be false after invite")
	}

	// Step 2: Verify invitation
	verifyReq := testutils.CreateJSONRequest(t, "GET", "/api/invitations/verify?token="+token, nil)
	rr = testutils.ExecuteRequest(t, invHandler.VerifyInvitation, verifyReq)
	rr.AssertStatusCode(http.StatusOK)
	rr.AssertBodyContains("invited@example.com")
	rr.AssertBodyContains("inviteduser")

	// Step 3: Accept invitation
	acceptReq := testutils.CreateJSONRequest(t, "POST", "/api/invitations/accept", map[string]interface{}{
		"token":    token,
		"password": "SecurePass1!",
	})
	rr = testutils.ExecuteRequest(t, invHandler.AcceptInvitation, acceptReq)
	rr.AssertStatusCode(http.StatusOK)
	rr.AssertBodyContains(`"status":"ok"`)

	// Step 4: Verify DB state
	var passwordHash string
	var requiresReset bool
	var emailVerified bool
	var isActive bool
	err = tdb.QueryRow(`
		SELECT password_hash, requires_password_reset, email_verified, is_active
		FROM users WHERE email = 'invited@example.com'
	`).Scan(&passwordHash, &requiresReset, &emailVerified, &isActive)
	if err != nil {
		t.Fatalf("Failed to query user: %v", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("SecurePass1!")); err != nil {
		t.Fatal("Password hash does not match expected password")
	}
	if requiresReset {
		t.Fatal("Expected requires_password_reset to be false")
	}
	if !emailVerified {
		t.Fatal("Expected email_verified to be true")
	}
	if !isActive {
		t.Fatal("Expected is_active to be true after accepting invitation")
	}

	// Verify invitation marked as used
	var usedAt sql.NullTime
	err = tdb.QueryRow(`
		SELECT i.used_at
		FROM user_invitations i
		JOIN users u ON u.id = i.user_id
		WHERE u.email = ?
	`, "invited@example.com").Scan(&usedAt)
	if err != nil {
		t.Fatalf("Failed to query invitation: %v", err)
	}
	if !usedAt.Valid {
		t.Fatal("Expected used_at to be set")
	}
}

func TestInvitationHandler_ExpiredToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	invService, permService := createInvitationTestServices(t, tdb)
	userHandler := newTestUserHandler(tdb, permService, invService)
	invHandler := NewInvitationHandler(invService)

	// Invite user and extract token
	inviteReq := testutils.CreateJSONRequest(t, "POST", "/api/users/invite", map[string]interface{}{
		"email":      "expired@example.com",
		"username":   "expireduser",
		"first_name": "Expired",
		"last_name":  "User",
		"is_active":  true,
	})
	rr := testutils.ExecuteAuthenticatedRequest(t, userHandler.InviteUser, inviteReq, nil)
	rr.AssertStatusCode(http.StatusCreated)
	token := rr.GetJSONField("token").(string)

	// Expire the token with an engine-neutral timestamp computed in Go
	expiredAt := time.Now().Add(-1 * time.Hour)
	result, err := tdb.Exec(`
		UPDATE user_invitations
		SET expires_at = ?
		WHERE user_id = (SELECT id FROM users WHERE email = ?)
	`, expiredAt, "expired@example.com")
	if err != nil {
		t.Fatalf("Failed to expire token: %v", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("Expected to expire 1 invitation, affected %d rows (error: %v)", rows, err)
	}

	// Verify should fail
	verifyReq := testutils.CreateJSONRequest(t, "GET", "/api/invitations/verify?token="+token, nil)
	rr = testutils.ExecuteRequest(t, invHandler.VerifyInvitation, verifyReq)
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("expired")

	// Accept should also fail
	acceptReq := testutils.CreateJSONRequest(t, "POST", "/api/invitations/accept", map[string]interface{}{
		"token":    token,
		"password": "SecurePass1!",
	})
	rr = testutils.ExecuteRequest(t, invHandler.AcceptInvitation, acceptReq)
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("expired")
}

func TestInvitationHandler_AlreadyUsedToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	invService, permService := createInvitationTestServices(t, tdb)
	userHandler := newTestUserHandler(tdb, permService, invService)
	invHandler := NewInvitationHandler(invService)

	// Invite and accept
	inviteReq := testutils.CreateJSONRequest(t, "POST", "/api/users/invite", map[string]interface{}{
		"email":      "used@example.com",
		"username":   "useduser",
		"first_name": "Used",
		"last_name":  "User",
		"is_active":  true,
	})
	rr := testutils.ExecuteAuthenticatedRequest(t, userHandler.InviteUser, inviteReq, nil)
	rr.AssertStatusCode(http.StatusCreated)
	token := rr.GetJSONField("token").(string)

	acceptReq := testutils.CreateJSONRequest(t, "POST", "/api/invitations/accept", map[string]interface{}{
		"token":    token,
		"password": "SecurePass1!",
	})
	rr = testutils.ExecuteRequest(t, invHandler.AcceptInvitation, acceptReq)
	rr.AssertStatusCode(http.StatusOK)

	// Capture password hash after first accept
	var hashAfterFirst string
	err := tdb.QueryRow(`SELECT password_hash FROM users WHERE email = 'used@example.com'`).Scan(&hashAfterFirst)
	if err != nil {
		t.Fatalf("Failed to query password_hash after first accept: %v", err)
	}

	// Verify again should fail
	verifyReq := testutils.CreateJSONRequest(t, "GET", "/api/invitations/verify?token="+token, nil)
	rr = testutils.ExecuteRequest(t, invHandler.VerifyInvitation, verifyReq)
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("already been used")

	// Accept again should fail
	acceptReq = testutils.CreateJSONRequest(t, "POST", "/api/invitations/accept", map[string]interface{}{
		"token":    token,
		"password": "AnotherPass1!",
	})
	rr = testutils.ExecuteRequest(t, invHandler.AcceptInvitation, acceptReq)
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("already been used")

	// Verify password was NOT changed by the duplicate accept attempt
	var hashAfterSecond string
	err = tdb.QueryRow(`SELECT password_hash FROM users WHERE email = 'used@example.com'`).Scan(&hashAfterSecond)
	if err != nil {
		t.Fatalf("Failed to query password_hash after second accept: %v", err)
	}
	if hashAfterSecond != hashAfterFirst {
		t.Fatal("Password hash changed after duplicate accept attempt — original password was overwritten")
	}
}

func TestInvitationHandler_InvalidToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	invService, _ := createInvitationTestServices(t, tdb)
	invHandler := NewInvitationHandler(invService)

	// Verify with bogus token
	verifyReq := testutils.CreateJSONRequest(t, "GET", "/api/invitations/verify?token=bogus_token_123", nil)
	rr := testutils.ExecuteRequest(t, invHandler.VerifyInvitation, verifyReq)
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("invalid")

	// Accept with bogus token
	acceptReq := testutils.CreateJSONRequest(t, "POST", "/api/invitations/accept", map[string]interface{}{
		"token":    "bogus_token_123",
		"password": "12345678",
	})
	rr = testutils.ExecuteRequest(t, invHandler.AcceptInvitation, acceptReq)
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("invalid")
}

func TestInvitationHandler_Validation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	invService, _ := createInvitationTestServices(t, tdb)
	invHandler := NewInvitationHandler(invService)

	tests := []struct {
		name    string
		body    interface{}
		message string
	}{
		{
			name:    "missing token",
			body:    map[string]string{"password": "SecurePass1!"},
			message: "token is required",
		},
		{
			name:    "missing password",
			body:    map[string]string{"token": "some-token"},
			message: "password is required",
		},
		{
			name:    "password too short",
			body:    map[string]string{"token": "some-token", "password": "short"},
			message: "at least 8 characters",
		},
		{
			name:    "empty body",
			body:    nil,
			message: "invalid request body",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, "POST", "/api/invitations/accept", tc.body)
			rr := testutils.ExecuteRequest(t, invHandler.AcceptInvitation, req)
			testutils.AssertValidationError(t, rr, tc.message)
		})
	}
}

func TestInvitationHandler_VerifyMissingToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	invService, _ := createInvitationTestServices(t, tdb)
	invHandler := NewInvitationHandler(invService)

	// When no ?token= param is provided, the handler falls back to the last
	// URL path segment. With a path like "/verify", that segment is "verify"
	// which is treated as a (bogus) token, yielding "invalid invitation token".
	req := testutils.CreateJSONRequest(t, "GET", "/api/invitations/verify", nil)
	rr := testutils.ExecuteRequest(t, invHandler.VerifyInvitation, req)
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("invalid")

	// A truly empty path segment (trailing slash) also falls back — the last
	// element is "", so token stays empty and we get "token is required".
	req = testutils.CreateJSONRequest(t, "GET", "/api/invitations/verify/?token=", nil)
	rr = testutils.ExecuteRequest(t, invHandler.VerifyInvitation, req)
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("token is required")
}
