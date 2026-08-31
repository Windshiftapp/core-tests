//go:build test

package services

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"

	"golang.org/x/crypto/bcrypt"
)

func createInvitedUser(t *testing.T, tdb *testutils.TestDB, email, username string) int {
	t.Helper()
	now := time.Now()
	var id int
	err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active, requires_password_reset, email_verified, created_at, updated_at)
		VALUES (?, ?, 'Test', 'Invited', false, true, false, ?, ?) RETURNING id
	`, email, username, now, now).Scan(&id)
	if err != nil {
		t.Fatalf("Failed to create invited user: %v", err)
	}
	return id
}

func TestInvitationService_GenerateAndVerify(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")
	userID := createInvitedUser(t, tdb, "gen@example.com", "genuser")

	// Generate
	token, err := svc.GenerateInvitation(userID)
	if err != nil {
		t.Fatalf("GenerateInvitation failed: %v", err)
	}
	if token == "" {
		t.Fatal("Expected non-empty token")
	}

	// Verify
	user, err := svc.VerifyInvitation(token)
	if err != nil {
		t.Fatalf("VerifyInvitation failed: %v", err)
	}
	if user.Email != "gen@example.com" {
		t.Fatalf("Expected email gen@example.com, got %s", user.Email)
	}
	if user.Username != "genuser" {
		t.Fatalf("Expected username genuser, got %s", user.Username)
	}
	if user.ID != userID {
		t.Fatalf("Expected user ID %d, got %d", userID, user.ID)
	}
}

func TestInvitationService_GenerateDoesNotStoreRawToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")
	userID := createInvitedUser(t, tdb, "hashed@example.com", "hasheduser")

	token, err := svc.GenerateInvitation(userID)
	if err != nil {
		t.Fatalf("GenerateInvitation failed: %v", err)
	}

	var storedToken string
	if err := tdb.QueryRow(`SELECT token FROM user_invitations WHERE user_id = ?`, userID).Scan(&storedToken); err != nil {
		t.Fatalf("Failed to query stored invitation token: %v", err)
	}
	if storedToken == token {
		t.Fatal("Invitation token was stored in plaintext")
	}

	if _, err := svc.VerifyInvitation(token); err != nil {
		t.Fatalf("VerifyInvitation failed for token stored in protected form: %v", err)
	}
}

func TestInvitationService_AcceptSetsPasswordAndMarksUsed(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")
	userID := createInvitedUser(t, tdb, "accept@example.com", "acceptuser")

	token, err := svc.GenerateInvitation(userID)
	if err != nil {
		t.Fatalf("GenerateInvitation failed: %v", err)
	}

	// Accept
	if err := svc.AcceptInvitation(token, "MyPassword1!"); err != nil {
		t.Fatalf("AcceptInvitation failed: %v", err)
	}

	// Verify user DB state
	var passwordHash string
	var requiresReset bool
	var emailVerified bool
	var isActive bool
	err = tdb.QueryRow(`
		SELECT password_hash, requires_password_reset, email_verified, is_active
		FROM users WHERE id = ?
	`, userID).Scan(&passwordHash, &requiresReset, &emailVerified, &isActive)
	if err != nil {
		t.Fatalf("Failed to query user: %v", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("MyPassword1!")); err != nil {
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

	// Verify invitation marked used
	var usedAt sql.NullTime
	err = tdb.QueryRow(`SELECT used_at FROM user_invitations WHERE user_id = ?`, userID).Scan(&usedAt)
	if err != nil {
		t.Fatalf("Failed to query invitation: %v", err)
	}
	if !usedAt.Valid {
		t.Fatal("Expected used_at to be set")
	}
}

func TestInvitationService_ExpiredToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")
	userID := createInvitedUser(t, tdb, "expire@example.com", "expireuser")

	token, err := svc.GenerateInvitation(userID)
	if err != nil {
		t.Fatalf("GenerateInvitation failed: %v", err)
	}

	// Expire the token
	result, err := tdb.Exec(`UPDATE user_invitations SET expires_at = ? WHERE user_id = ?`, time.Now().UTC().Add(-time.Hour), userID)
	if err != nil {
		t.Fatalf("Failed to expire token: %v", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("Expected to expire 1 invitation, affected %d rows (error: %v)", rows, err)
	}

	// Verify should return ErrInvitationExpired
	_, err = svc.VerifyInvitation(token)
	if err != ErrInvitationExpired {
		t.Fatalf("Expected ErrInvitationExpired, got: %v", err)
	}

	// Accept should also return ErrInvitationExpired
	err = svc.AcceptInvitation(token, "SomePass1!")
	if err != ErrInvitationExpired {
		t.Fatalf("Expected ErrInvitationExpired, got: %v", err)
	}
}

func TestInvitationService_AlreadyUsedToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")
	userID := createInvitedUser(t, tdb, "reuse@example.com", "reuseuser")

	token, err := svc.GenerateInvitation(userID)
	if err != nil {
		t.Fatalf("GenerateInvitation failed: %v", err)
	}

	// Accept once (should succeed)
	if err := svc.AcceptInvitation(token, "FirstPass1!"); err != nil {
		t.Fatalf("First AcceptInvitation failed: %v", err)
	}

	// Capture password hash after first accept
	var hashAfterFirst string
	err = tdb.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hashAfterFirst)
	if err != nil {
		t.Fatalf("Failed to query password_hash after first accept: %v", err)
	}

	// Verify again should return ErrInvitationAlreadyUsed
	_, err = svc.VerifyInvitation(token)
	if err != ErrInvitationAlreadyUsed {
		t.Fatalf("Expected ErrInvitationAlreadyUsed, got: %v", err)
	}

	// Accept again should return ErrInvitationAlreadyUsed
	err = svc.AcceptInvitation(token, "SecondPass1!")
	if err != ErrInvitationAlreadyUsed {
		t.Fatalf("Expected ErrInvitationAlreadyUsed, got: %v", err)
	}

	// Verify password was NOT changed by the duplicate accept attempt
	var hashAfterSecond string
	err = tdb.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hashAfterSecond)
	if err != nil {
		t.Fatalf("Failed to query password_hash after second accept: %v", err)
	}
	if hashAfterSecond != hashAfterFirst {
		t.Fatal("Password hash changed after duplicate accept attempt — original password was overwritten")
	}
}

func TestInvitationService_InvalidToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")

	_, err := svc.VerifyInvitation("nonexistent_token")
	if err != ErrInvitationInvalid {
		t.Fatalf("Expected ErrInvitationInvalid, got: %v", err)
	}
}

func TestInvitationService_GenerateInvalidatesPriorTokens(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")
	userID := createInvitedUser(t, tdb, "stale@example.com", "staleuser")

	firstToken, err := svc.GenerateInvitation(userID)
	if err != nil {
		t.Fatalf("first GenerateInvitation failed: %v", err)
	}

	secondToken, err := svc.GenerateInvitation(userID)
	if err != nil {
		t.Fatalf("second GenerateInvitation failed: %v", err)
	}

	// First token must no longer verify — issuing a fresh invitation
	// invalidates any prior outstanding token for the same user.
	if _, err := svc.VerifyInvitation(firstToken); err != ErrInvitationAlreadyUsed {
		t.Fatalf("expected first token to be invalidated (ErrInvitationAlreadyUsed), got: %v", err)
	}

	// Second token still verifies and accepts cleanly.
	if _, err := svc.VerifyInvitation(secondToken); err != nil {
		t.Fatalf("expected second token to still verify, got: %v", err)
	}
	if err := svc.AcceptInvitation(secondToken, "FreshPass1!"); err != nil {
		t.Fatalf("AcceptInvitation on second token failed: %v", err)
	}

	// Stale first token must not be usable to overwrite the password after
	// the account is activated.
	if err := svc.AcceptInvitation(firstToken, "AttackerPass1!"); err != ErrInvitationAlreadyUsed {
		t.Fatalf("expected first token to fail AcceptInvitation with ErrInvitationAlreadyUsed, got: %v", err)
	}
}

func TestInvitationService_RejectAcceptForActiveUser(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")
	userID := createInvitedUser(t, tdb, "active@example.com", "activeuser")

	token, err := svc.GenerateInvitation(userID)
	if err != nil {
		t.Fatalf("GenerateInvitation failed: %v", err)
	}

	// Simulate the account being activated through a different code path
	// (e.g. SSO bootstrap) after the invitation was issued but before it
	// was redeemed.
	if _, err := tdb.Exec(`UPDATE users SET is_active = true WHERE id = ?`, userID); err != nil {
		t.Fatalf("failed to activate user: %v", err)
	}

	if err := svc.AcceptInvitation(token, "ShouldNotApply1!"); err != ErrInvitationAlreadyUsed {
		t.Fatalf("expected ErrInvitationAlreadyUsed for an already-active user, got: %v", err)
	}

	// Password must remain unset — the stale invitation must not have
	// rewritten it.
	var hash sql.NullString
	if err := tdb.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		t.Fatalf("failed to query user: %v", err)
	}
	if hash.Valid && hash.String != "" {
		t.Fatalf("expected password_hash to remain unset, got non-empty hash")
	}
}

func TestInvitationService_CleanupExpiredInvitations(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")

	userA := createInvitedUser(t, tdb, "cleanA@example.com", "cleanausr")
	userB := createInvitedUser(t, tdb, "cleanB@example.com", "cleanbusr")

	_, err := svc.GenerateInvitation(userA)
	if err != nil {
		t.Fatalf("GenerateInvitation for A failed: %v", err)
	}

	tokenB, err := svc.GenerateInvitation(userB)
	if err != nil {
		t.Fatalf("GenerateInvitation for B failed: %v", err)
	}

	// Expire invitation A (more than 24h past expiry)
	result, err := tdb.Exec(`UPDATE user_invitations SET expires_at = ? WHERE user_id = ?`, time.Now().UTC().Add(-25*time.Hour), userA)
	if err != nil {
		t.Fatalf("Failed to expire token A: %v", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("Expected to expire 1 invitation, affected %d rows (error: %v)", rows, err)
	}

	// Accept invitation B (marks it as used)
	if err := svc.AcceptInvitation(tokenB, "CleanPass1!"); err != nil {
		t.Fatalf("AcceptInvitation for B failed: %v", err)
	}

	// Run cleanup
	if err := svc.CleanupExpiredInvitations(); err != nil {
		t.Fatalf("CleanupExpiredInvitations failed: %v", err)
	}

	// Both invitations should be removed
	var count int
	err = tdb.QueryRow(`SELECT COUNT(*) FROM user_invitations WHERE user_id IN (?, ?)`, userA, userB).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count invitations: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected 0 invitations after cleanup, got %d", count)
	}

	// Users should still exist
	var userCount int
	err = tdb.QueryRow(`SELECT COUNT(*) FROM users WHERE id IN (?, ?)`, userA, userB).Scan(&userCount)
	if err != nil {
		t.Fatalf("Failed to count users: %v", err)
	}
	if userCount != 2 {
		t.Fatalf("Expected 2 users to still exist, got %d", userCount)
	}
}

func TestInvitationService_SendRefusesAgentRecipient(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// nil smtpSender is fine — the agent guard fires before the SMTP check.
	svc := NewInvitationService(tdb.GetDatabase(), nil, "http://test.local")
	agent := &models.User{ID: 999, Email: "agent@example.com", IsAgent: true}

	err := svc.SendInvitationEmail(agent, "any-token")
	if !errors.Is(err, ErrRecipientIsAgent) {
		t.Fatalf("expected ErrRecipientIsAgent, got %v", err)
	}
}

func TestEmailVerificationService_SendRefusesAgentRecipient(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	svc := NewEmailVerificationService(tdb.GetDatabase(), nil, "http://test.local")
	agent := &models.User{ID: 999, Email: "agent@example.com", IsAgent: true}

	err := svc.SendVerificationEmail(agent, "any-token")
	if !errors.Is(err, ErrRecipientIsAgent) {
		t.Fatalf("expected ErrRecipientIsAgent, got %v", err)
	}
}
