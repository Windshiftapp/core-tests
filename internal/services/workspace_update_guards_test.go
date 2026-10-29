//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// Regression tests for the 0.8.9 workspace update guards:
// personal workspaces cannot be deactivated (WI-1382) and only personal
// workspaces may change their key (WI-1383).

func newWorkspaceGuardService(t *testing.T) (*WorkspaceService, *testutils.TestDB, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	return NewWorkspaceService(tdb.GetDatabase()), tdb, data.UserID
}

// seedPersonalWorkspaceRow inserts a personal workspace directly. The
// handler-level get-or-create is covered in the handlers package; the guard
// tests only need a personal row to exist.
func seedPersonalWorkspaceRow(t *testing.T, tdb *testutils.TestDB, ownerID int) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active, is_personal, owner_id, description)
		VALUES ('My Todo List', 'MINE1', true, true, ?, '')
		RETURNING id
	`, ownerID).Scan(&id); err != nil {
		t.Fatalf("seed personal workspace: %v", err)
	}
	return id
}

func TestWorkspaceUpdateRejectsDeactivatingPersonalWorkspace(t *testing.T) {
	svc, tdb, userID := newWorkspaceGuardService(t)
	personalID := seedPersonalWorkspaceRow(t, tdb, userID)

	inactive := false
	_, err := svc.Update(UpdateWorkspaceParams{ID: personalID, Active: &inactive})
	if !errors.Is(err, ErrPersonalWorkspaceDeactivation) {
		t.Fatalf("deactivating a personal workspace: err=%v, want ErrPersonalWorkspaceDeactivation", err)
	}

	// Regular workspaces deactivate fine.
	inactiveRegular := false
	if _, err := svc.Update(UpdateWorkspaceParams{ID: 1, Active: &inactiveRegular}); err != nil {
		t.Errorf("deactivating a regular workspace errored: %v", err)
	}
}

func TestWorkspaceUpdateKeyOnlyForPersonalWorkspaces(t *testing.T) {
	svc, tdb, userID := newWorkspaceGuardService(t)
	personalID := seedPersonalWorkspaceRow(t, tdb, userID)

	newKey := "PFOO"
	if _, err := svc.Update(UpdateWorkspaceParams{ID: personalID, Key: &newKey}); err != nil {
		t.Fatalf("changing a personal workspace key errored: %v", err)
	}
	updated, err := svc.GetByID(personalID)
	if err != nil {
		t.Fatalf("reload personal workspace: %v", err)
	}
	if updated.Key != newKey {
		t.Errorf("personal workspace key = %q, want %q", updated.Key, newKey)
	}

	// Regular workspace keys stay immutable.
	regularKey := "SHRD"
	_, err = svc.Update(UpdateWorkspaceParams{ID: 1, Key: &regularKey})
	if !errors.Is(err, ErrWorkspaceKeyImmutable) {
		t.Fatalf("changing a regular workspace key: err=%v, want ErrWorkspaceKeyImmutable", err)
	}
}

func TestWorkspaceUpdatePersonalKeyConflictIsDuplicateEntry(t *testing.T) {
	svc, tdb, userID := newWorkspaceGuardService(t)
	personalID := seedPersonalWorkspaceRow(t, tdb, userID)

	// A pre-existing workspace already owns the requested key.
	if _, err := tdb.Exec(`
		INSERT INTO workspaces (name, key, active, description) VALUES ('Squatter', 'SHRD', true, '')
	`); err != nil {
		t.Fatalf("seed squatter workspace: %v", err)
	}

	dup := "SHRD"
	_, err := svc.Update(UpdateWorkspaceParams{ID: personalID, Key: &dup})
	if !errors.Is(err, repository.ErrDuplicateEntry) {
		t.Fatalf("conflicting personal key rename: err=%v, want ErrDuplicateEntry", err)
	}
}
