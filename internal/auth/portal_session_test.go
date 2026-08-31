package auth

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
)

func TestPortalSessionManagerHashesTokensAndSupportsLegacySessions(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "portal-sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	channelResult, err := db.ExecWrite(`
		INSERT INTO channels (name, type, direction, status)
		VALUES (?, ?, ?, ?)
	`, "Customer portal", "portal", "inbound", "enabled")
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	channelID64, _ := channelResult.LastInsertId()
	customerResult, err := db.ExecWrite(`
		INSERT INTO portal_customers (name, email) VALUES (?, ?)
	`, "Portal User", "portal-session@example.com")
	if err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}
	customerID64, _ := customerResult.LastInsertId()
	channelID, customerID := int(channelID64), int(customerID64)

	sm := NewPortalSessionManager(db, false, false, nil, "portal-test-secret", "strict")
	session, err := sm.CreatePortalSession(customerID, channelID, "198.51.100.20:4321", "test-agent")
	if err != nil {
		t.Fatalf("CreatePortalSession: %v", err)
	}

	var stored string
	if err := db.QueryRow(`SELECT session_token FROM portal_customer_sessions WHERE id = ?`, session.ID).Scan(&stored); err != nil {
		t.Fatalf("query stored token: %v", err)
	}
	if stored == session.Token {
		t.Fatal("portal session token was stored in plaintext")
	}
	if !strings.HasPrefix(stored, sessionTokenHashPrefix) {
		t.Fatalf("stored token %q missing hash prefix", stored)
	}

	encoded, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal portal session: %v", err)
	}
	if strings.Contains(string(encoded), session.Token) || strings.Contains(string(encoded), `"token"`) {
		t.Fatalf("portal session serialized token: %s", encoded)
	}

	validated, err := sm.ValidatePortalSession(session.Token, "198.51.100.20")
	if err != nil {
		t.Fatalf("ValidatePortalSession hashed token: %v", err)
	}
	if validated.ChannelID == nil || *validated.ChannelID != channelID {
		t.Fatalf("channel binding = %v, want %d", validated.ChannelID, channelID)
	}
	if _, err := sm.ValidatePortalSession("wrong-token", "198.51.100.20"); !errors.Is(err, ErrPortalSessionNotFound) {
		t.Fatalf("wrong token error = %v, want ErrPortalSessionNotFound", err)
	}
	if _, err := sm.ValidatePortalSession(session.Token, "203.0.113.50"); !errors.Is(err, ErrPortalSessionInvalid) {
		t.Fatalf("wrong IP error = %v, want ErrPortalSessionInvalid", err)
	}

	if err := sm.DeletePortalSession(session.Token); err != nil {
		t.Fatalf("DeletePortalSession hashed token: %v", err)
	}
	if _, err := sm.ValidatePortalSession(session.Token, "198.51.100.20"); !errors.Is(err, ErrPortalSessionNotFound) {
		t.Fatalf("deleted session error = %v, want ErrPortalSessionNotFound", err)
	}

	legacyToken := "legacy-portal-plaintext-token"
	_, err = db.ExecWrite(`
		INSERT INTO portal_customer_sessions
			(portal_customer_id, session_token, channel_id, expires_at, ip_address, user_agent, is_active)
		VALUES (?, ?, ?, ?, ?, ?, true)
	`, customerID, legacyToken, channelID, time.Now().Add(time.Hour), "198.51.100.20", "legacy-agent")
	if err != nil {
		t.Fatalf("insert legacy portal session: %v", err)
	}
	if _, err := sm.ValidatePortalSession(legacyToken, "198.51.100.20"); err != nil {
		t.Fatalf("ValidatePortalSession legacy token: %v", err)
	}
	if err := sm.DeletePortalSession(legacyToken); err != nil {
		t.Fatalf("DeletePortalSession legacy token: %v", err)
	}

	expiredToken := "expired-portal-token"
	_, err = db.ExecWrite(`
		INSERT INTO portal_customer_sessions
			(portal_customer_id, session_token, channel_id, expires_at, ip_address, user_agent, is_active)
		VALUES (?, ?, ?, ?, ?, ?, true)
	`, customerID, hashSessionToken(expiredToken), channelID, time.Now().Add(-time.Minute), "198.51.100.20", "expired-agent")
	if err != nil {
		t.Fatalf("insert expired portal session: %v", err)
	}
	if _, err := sm.ValidatePortalSession(expiredToken, "198.51.100.20"); !errors.Is(err, ErrPortalSessionExpired) {
		t.Fatalf("expired session error = %v, want ErrPortalSessionExpired", err)
	}
}
