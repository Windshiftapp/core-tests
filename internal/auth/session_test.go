package auth

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
)

func TestSessionManagerStoresHashedTokensAndValidatesLegacyPlaintext(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	res, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active, email_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, true, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, "session-test@example.com", "session-test", "Session", "Test")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID64, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	userID := int(userID64)

	sm := NewSessionManager(db, false, false, nil, "test-cookie-secret", "strict")
	sameSecretManager := NewSessionManager(db, false, false, nil, "test-cookie-secret", "strict")
	if !bytes.Equal(sm.DeriveOpaqueValue("test", "value"), sameSecretManager.DeriveOpaqueValue("test", "value")) {
		t.Fatal("opaque auth values were not stable for the configured secret")
	}
	if bytes.Equal(sm.DeriveOpaqueValue("test", "value"), sm.DeriveOpaqueValue("other", "value")) {
		t.Fatal("opaque auth values were not purpose-scoped")
	}

	session, err := sm.CreateSession(userID, "198.51.100.10", "test-agent", false)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var stored string
	if err := db.QueryRow(`SELECT session_token FROM user_sessions WHERE id = ?`, session.ID).Scan(&stored); err != nil {
		t.Fatalf("query stored token: %v", err)
	}
	if stored == session.Token {
		t.Fatal("session_token stored plaintext token")
	}
	if !strings.HasPrefix(stored, sessionTokenHashPrefix) {
		t.Fatalf("stored token %q missing hash prefix", stored)
	}
	validated, err := sm.ValidateSession(session.Token, "198.51.100.10")
	if err != nil {
		t.Fatalf("ValidateSession hashed token: %v", err)
	}
	if validated.EnrollmentRequired {
		t.Fatal("new normal session unexpectedly requires enrollment")
	}
	if err := sm.SetEnrollmentRequired(session.ID, true); err != nil {
		t.Fatalf("SetEnrollmentRequired: %v", err)
	}
	validated, err = sm.ValidateSession(session.Token, "198.51.100.10")
	if err != nil {
		t.Fatalf("ValidateSession restricted token: %v", err)
	}
	if !validated.EnrollmentRequired || validated.AuthPendingType != AuthPendingEnrollment {
		t.Fatalf("restricted session state = required %v, type %q", validated.EnrollmentRequired, validated.AuthPendingType)
	}

	verificationSession, err := sm.CreateSession(userID, "198.51.100.10", "verification-agent", false)
	if err != nil {
		t.Fatalf("CreateSession verification: %v", err)
	}
	if err := sm.SetAuthPending(verificationSession.ID, AuthPendingPasskeyVerification); err != nil {
		t.Fatalf("SetAuthPending verification: %v", err)
	}
	if err := sm.ClearEnrollmentRequiredByUserID(userID); err != nil {
		t.Fatalf("ClearEnrollmentRequiredByUserID: %v", err)
	}
	validated, err = sm.ValidateSession(session.Token, "198.51.100.10")
	if err != nil {
		t.Fatalf("ValidateSession enrollment after clear: %v", err)
	}
	if validated.EnrollmentRequired {
		t.Fatal("enrollment session was not elevated")
	}
	validated, err = sm.ValidateSession(verificationSession.Token, "198.51.100.10")
	if err != nil {
		t.Fatalf("ValidateSession verification: %v", err)
	}
	if !validated.EnrollmentRequired || validated.AuthPendingType != AuthPendingPasskeyVerification {
		t.Fatalf("verification session was incorrectly elevated: required %v, type %q", validated.EnrollmentRequired, validated.AuthPendingType)
	}

	legacyToken := "legacy-plaintext-session-token"
	_, err = db.ExecWrite(`
		INSERT INTO user_sessions (user_id, session_token, expires_at, ip_address, user_agent, is_active, created_at)
		VALUES (?, ?, ?, ?, ?, true, CURRENT_TIMESTAMP)
	`, userID, legacyToken, time.Now().Add(time.Hour), "198.51.100.10", "legacy-agent")
	if err != nil {
		t.Fatalf("insert legacy session: %v", err)
	}
	if _, err := sm.ValidateSession(legacyToken, "198.51.100.10"); err != nil {
		t.Fatalf("ValidateSession legacy plaintext token: %v", err)
	}
}
