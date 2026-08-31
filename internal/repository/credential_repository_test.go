package repository

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func TestCredentialRepositoryHasActiveFIDOIncludesModernWebAuthn(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "credentials.db"))
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
	`, "passkey@example.com", "passkey", "Pass", "Key")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID64, _ := result.LastInsertId()
	userID := int(userID64)

	repo := NewCredentialRepository(db)
	if has, err := repo.HasActiveFIDO(userID); err != nil || has {
		t.Fatalf("HasActiveFIDO before insert = %v, %v; want false, nil", has, err)
	}
	_, err = db.ExecWrite(`
		INSERT INTO webauthn_credentials (id, user_id, credential_name, public_key)
		VALUES (?, ?, ?, ?)
	`, "modern-credential", userID, "Modern passkey", []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("insert WebAuthn credential: %v", err)
	}
	if has, err := repo.HasActiveFIDO(userID); err != nil || !has {
		t.Fatalf("HasActiveFIDO after insert = %v, %v; want true, nil", has, err)
	}
}
