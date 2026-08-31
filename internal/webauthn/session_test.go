package webauthn

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestRegistrationSessionRequiresMatchingUserAndIsConsumedOnce(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	defer func() { _ = db.Close() }()

	var otherUserID int
	err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, "other@example.com", "other", "Other", "User").Scan(&otherUserID)
	if err != nil {
		t.Fatalf("insert other user: %v", err)
	}

	store := NewSessionStore(db)
	data := &webauthnlib.SessionData{Challenge: "registration-challenge", UserID: []byte("1")}
	sessionID, err := store.SaveRegistrationSession(1, data)
	if err != nil {
		t.Fatalf("SaveRegistrationSession: %v", err)
	}

	if _, err := store.GetRegistrationSession(sessionID, otherUserID); err == nil {
		t.Fatal("different user consumed registration challenge")
	}
	got, err := store.GetRegistrationSession(sessionID, 1)
	if err != nil {
		t.Fatalf("GetRegistrationSession for owner: %v", err)
	}
	if got.Challenge != data.Challenge {
		t.Fatalf("challenge = %q, want %q", got.Challenge, data.Challenge)
	}
	if _, err := store.GetRegistrationSession(sessionID, 1); err == nil {
		t.Fatal("registration challenge was reusable")
	}
}

func TestAuthenticationSessionConcurrentConsume(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "webauthn-race.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	result, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, "race@example.com", "race", "Race", "Test")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID64, _ := result.LastInsertId()
	userID := int(userID64)

	// Widen the read-then-delete window so a non-atomic consume is observable.
	// The trigger fires only for rows that actually match, so once the row is
	// consumed it adds no cost to the remaining (zero-row) deletes.
	if _, err := db.Exec(`
		CREATE TRIGGER slow_session_delete BEFORE DELETE ON webauthn_sessions
		BEGIN
			SELECT hex(randomblob(2000000));
		END
	`); err != nil {
		t.Fatalf("create slow trigger: %v", err)
	}

	store := NewSessionStore(db)
	data := &webauthnlib.SessionData{Challenge: "race-challenge", UserID: []byte("1")}
	sessionID, err := store.SaveAuthenticationSession(&userID, data)
	if err != nil {
		t.Fatalf("SaveAuthenticationSession: %v", err)
	}

	const workers = 32
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	var successes atomic.Int64
	ready.Add(workers)
	done.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			if _, err := store.GetAuthenticationSession(sessionID); err == nil {
				successes.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful challenge consumptions = %d, want exactly 1 (one-time challenge was consumed multiple times)", got)
	}
}

func TestAuthenticationSessionBinding(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "webauthn-sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	result, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, "bound-session@example.com", "bound-session", "Bound", "Session")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID64, _ := result.LastInsertId()
	userID := int(userID64)

	store := NewSessionStore(db)
	data := &webauthnlib.SessionData{Challenge: "bound-challenge", UserID: []byte("1")}
	sessionID, err := store.SaveAuthenticationSessionBound(userID, 42, data)
	if err != nil {
		t.Fatalf("SaveAuthenticationSessionBound: %v", err)
	}
	if _, err := store.GetAuthenticationSession(sessionID); err == nil {
		t.Fatal("unbound lookup consumed a bound authentication challenge")
	}
	if _, err := store.GetAuthenticationSessionBound(sessionID, 41); err == nil {
		t.Fatal("different pending session consumed a bound authentication challenge")
	}
	got, err := store.GetAuthenticationSessionBound(sessionID, 42)
	if err != nil {
		t.Fatalf("GetAuthenticationSessionBound: %v", err)
	}
	if got.Challenge != data.Challenge {
		t.Fatalf("challenge = %q, want %q", got.Challenge, data.Challenge)
	}
	if _, err := store.GetAuthenticationSessionBound(sessionID, 42); err == nil {
		t.Fatal("bound authentication challenge was reusable")
	}
}
