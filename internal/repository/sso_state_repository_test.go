package repository

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
)

func TestSSOStateRepositoryPreservesRememberMeAndEnforcesExpiry(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "sso-state.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	result, err := db.ExecWrite(`
		INSERT INTO sso_providers (slug, name, provider_type, enabled)
		VALUES ('test-oidc', 'Test OIDC', 'oidc', true)
	`)
	if err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	providerID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	repo := NewSSOStateRepository(db)
	if err := repo.Store(int(providerID), "remembered-state", "", "/after-login", true, now.Add(15*time.Minute)); err != nil {
		t.Fatalf("Store remembered state: %v", err)
	}

	token, err := repo.GetValid("remembered-state", int(providerID), now.Add(14*time.Minute))
	if err != nil {
		t.Fatalf("GetValid before expiry: %v", err)
	}
	if !token.RememberMe || token.RedirectURI != "/after-login" {
		t.Fatalf("state metadata = %+v", token)
	}
	if _, err := repo.GetValid("remembered-state", int(providerID), now.Add(16*time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetValid after expiry error = %v, want sql.ErrNoRows", err)
	}

	if err := repo.Delete(token.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetValid("remembered-state", int(providerID), now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetValid after delete error = %v, want sql.ErrNoRows", err)
	}
}
