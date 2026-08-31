package repository

import (
	"errors"
	"testing"
	"time"

	"windshift/internal/database"
)

// nativeAuthCodesSchema mirrors the catalog migration (inline_native_auth_codes)
// so the repository round-trips against the same shape it sees in production.
const nativeAuthCodesSchema = `
	CREATE TABLE native_auth_codes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT NOT NULL UNIQUE,
		session_token TEXT NOT NULL,
		session_expires_at DATETIME NOT NULL,
		status TEXT NOT NULL DEFAULT 'valid',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME NOT NULL,
		consumed_at DATETIME
	);
`

func newNativeAuthTestRepo(t *testing.T) *NativeAuthRepository {
	t.Helper()
	db, err := database.NewSQLiteDB("file::memory:?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(nativeAuthCodesSchema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	return NewNativeAuthRepository(db)
}

func TestNativeAuthRepository_ConsumeRedeemsOnce(t *testing.T) {
	repo := newNativeAuthTestRepo(t)
	now := time.Now()
	sessionExp := now.Add(24 * time.Hour)

	if err := repo.Store("code-abc", "sess-token-xyz", sessionExp, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, err := repo.Consume("code-abc", now)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.SessionToken != "sess-token-xyz" {
		t.Errorf("SessionToken = %q, want %q", got.SessionToken, "sess-token-xyz")
	}
	if got.SessionExpiresAt.Unix() != sessionExp.Unix() {
		t.Errorf("SessionExpiresAt = %v, want %v", got.SessionExpiresAt, sessionExp)
	}

	// Single-use: a replay must fail closed.
	if _, err := repo.Consume("code-abc", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("replay Consume err = %v, want ErrNotFound", err)
	}
}

func TestNativeAuthRepository_ConsumeUnknownCode(t *testing.T) {
	repo := newNativeAuthTestRepo(t)
	if _, err := repo.Consume("missing", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Consume(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestNativeAuthRepository_ConsumeExpiredCode(t *testing.T) {
	repo := newNativeAuthTestRepo(t)
	now := time.Now()
	// Code expired one minute ago.
	if err := repo.Store("stale", "tok", now.Add(time.Hour), now.Add(-time.Minute)); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if _, err := repo.Consume("stale", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("Consume(expired) err = %v, want ErrNotFound", err)
	}
}
