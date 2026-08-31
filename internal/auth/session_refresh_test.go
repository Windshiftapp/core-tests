//go:build test

package auth_test

import (
	"errors"
	"testing"
	"time"

	"windshift/internal/auth"
	"windshift/internal/testutils"
)

func TestRefreshSessionDoesNotExtendExpiredRow(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })

	var userID int
	if err := db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find fixture user: %v", err)
	}
	manager := auth.NewSessionManager(db, false, false, nil, "session-refresh-test-secret", "strict")
	session, err := manager.CreateSession(userID, "198.51.100.30", "test-agent", false)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// The API cannot create an expired active session, so set the timestamp directly.
	expiredAt := time.Now().UTC().Add(-time.Hour)
	if _, err := db.ExecWrite(`UPDATE user_sessions SET expires_at = ? WHERE id = ?`, expiredAt, session.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	if err := manager.RefreshSession(session.Token, true); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("RefreshSession error = %v, want ErrInvalidSession", err)
	}
	var persistedExpiry time.Time
	if err := db.QueryRow(`SELECT expires_at FROM user_sessions WHERE id = ?`, session.ID).Scan(&persistedExpiry); err != nil {
		t.Fatalf("read session expiry: %v", err)
	}
	if persistedExpiry.After(expiredAt.Add(time.Second)) {
		t.Fatalf("expired session was extended from %v to %v", expiredAt, persistedExpiry)
	}
}
