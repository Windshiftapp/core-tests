package repository

import (
	"errors"
	"sort"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

// TestActionCredentialRepository covers the credential CRUD path: scope flag,
// workspace allowlist join, rotation, metadata-only update, and the
// "list for workspace" view that mixes universal + scoped credentials.
func TestActionCredentialRepository(t *testing.T) {
	db, err := database.NewSQLiteDB("file::memory:?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE workspaces (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT);
		CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT);
		CREATE TABLE action_credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			credential_type TEXT NOT NULL,
			applies_to_all_workspaces BOOLEAN NOT NULL DEFAULT TRUE,
			created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
			encrypted_secret TEXT NOT NULL,
			secret_prefix TEXT,
			secret_metadata TEXT,
			is_enabled BOOLEAN DEFAULT TRUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE action_credential_workspaces (
			credential_id INTEGER NOT NULL REFERENCES action_credentials(id) ON DELETE CASCADE,
			workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			PRIMARY KEY (credential_id, workspace_id)
		);
		INSERT INTO workspaces (id, name) VALUES (1, 'alpha'), (2, 'beta'), (3, 'gamma');
		INSERT INTO users (id) VALUES (10);
	`); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	repo := NewActionCredentialRepository(db)
	creator := 10

	var globalID, alphaOnlyID, alphaAndBetaID int

	t.Run("Create applies-to-all", func(t *testing.T) {
		c := &models.ActionCredential{
			Name:                   "GitHub global",
			CredentialType:         models.CredentialBearerToken,
			AppliesToAllWorkspaces: true,
			CreatedBy:              &creator,
			EncryptedSecret:        "ciphertext-global",
			SecretPrefix:           "ghp_…",
			IsEnabled:              true,
		}
		id, err := repo.CreateActionCredential(c)
		if err != nil {
			t.Fatalf("create global: %v", err)
		}
		globalID = id
		got, err := repo.GetActionCredentialByID(id)
		if err != nil {
			t.Fatalf("get global: %v", err)
		}
		if !got.AppliesToAllWorkspaces {
			t.Errorf("AppliesToAllWorkspaces should be true")
		}
		if len(got.WorkspaceIDs) != 0 {
			t.Errorf("WorkspaceIDs should be empty for global, got %v", got.WorkspaceIDs)
		}
		if got.EncryptedSecret != "ciphertext-global" {
			t.Errorf("EncryptedSecret mismatch")
		}
	})

	t.Run("Create scoped to single workspace", func(t *testing.T) {
		c := &models.ActionCredential{
			Name:                   "alpha token",
			CredentialType:         models.CredentialAPIKey,
			AppliesToAllWorkspaces: false,
			CreatedBy:              &creator,
			EncryptedSecret:        "ciphertext-alpha",
			SecretMetadata:         `{"provider":"linear"}`,
			IsEnabled:              true,
		}
		id, err := repo.CreateActionCredential(c)
		if err != nil {
			t.Fatalf("create scoped: %v", err)
		}
		alphaOnlyID = id
		if err := repo.SetCredentialWorkspaces(id, []int{1}); err != nil {
			t.Fatalf("set workspaces: %v", err)
		}
		got, err := repo.GetActionCredentialByID(id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.AppliesToAllWorkspaces {
			t.Errorf("AppliesToAllWorkspaces should be false")
		}
		if want := []int{1}; !equalInts(got.WorkspaceIDs, want) {
			t.Errorf("WorkspaceIDs = %v, want %v", got.WorkspaceIDs, want)
		}
	})

	t.Run("Create scoped to multiple workspaces", func(t *testing.T) {
		c := &models.ActionCredential{
			Name:                   "alpha+beta token",
			CredentialType:         models.CredentialAPIKey,
			AppliesToAllWorkspaces: false,
			CreatedBy:              &creator,
			EncryptedSecret:        "ciphertext-multi",
			IsEnabled:              true,
		}
		id, err := repo.CreateActionCredential(c)
		if err != nil {
			t.Fatalf("create multi: %v", err)
		}
		alphaAndBetaID = id
		if err := repo.SetCredentialWorkspaces(id, []int{1, 2}); err != nil {
			t.Fatalf("set workspaces: %v", err)
		}
		got, err := repo.GetActionCredentialByID(id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if want := []int{1, 2}; !equalInts(got.WorkspaceIDs, want) {
			t.Errorf("WorkspaceIDs = %v, want %v", got.WorkspaceIDs, want)
		}
	})

	t.Run("Rejects empty ciphertext", func(t *testing.T) {
		_, err := repo.CreateActionCredential(&models.ActionCredential{
			Name:           "bad",
			CredentialType: models.CredentialBearerToken,
		})
		if err == nil {
			t.Fatalf("expected error on empty ciphertext")
		}
	})

	t.Run("IsCredentialScopedToWorkspace", func(t *testing.T) {
		// applies-to-all credential is reachable from every workspace.
		ok, err := repo.IsCredentialScopedToWorkspace(globalID, 3)
		if err != nil || !ok {
			t.Errorf("global should be in scope for ws3: ok=%v err=%v", ok, err)
		}
		// alpha-only credential reachable from alpha, not beta.
		ok, _ = repo.IsCredentialScopedToWorkspace(alphaOnlyID, 1)
		if !ok {
			t.Errorf("alpha-only should be in scope for ws1")
		}
		ok, _ = repo.IsCredentialScopedToWorkspace(alphaOnlyID, 2)
		if ok {
			t.Errorf("alpha-only should NOT be in scope for ws2")
		}
		// alpha+beta credential reachable from both, not gamma.
		ok, _ = repo.IsCredentialScopedToWorkspace(alphaAndBetaID, 2)
		if !ok {
			t.Errorf("alpha+beta should be in scope for ws2")
		}
		ok, _ = repo.IsCredentialScopedToWorkspace(alphaAndBetaID, 3)
		if ok {
			t.Errorf("alpha+beta should NOT be in scope for ws3")
		}
		// Unknown ID surfaces ErrNotFound.
		_, err = repo.IsCredentialScopedToWorkspace(99999, 1)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown id should return ErrNotFound, got %v", err)
		}
	})

	t.Run("ListForWorkspace mixes global + scoped", func(t *testing.T) {
		list, err := repo.ListActionCredentialsForWorkspace(1)
		if err != nil {
			t.Fatalf("list ws1: %v", err)
		}
		// alpha sees: global, alpha-only, alpha+beta
		if len(list) != 3 {
			t.Fatalf("want 3 for ws1, got %d", len(list))
		}

		list2, _ := repo.ListActionCredentialsForWorkspace(2)
		// beta sees: global, alpha+beta
		if len(list2) != 2 {
			t.Fatalf("want 2 for ws2, got %d", len(list2))
		}

		list3, _ := repo.ListActionCredentialsForWorkspace(3)
		// gamma sees: global only
		if len(list3) != 1 {
			t.Fatalf("want 1 for ws3, got %d", len(list3))
		}

		// Scoped rows in the list have their WorkspaceIDs populated.
		for _, c := range list {
			if c.ID == alphaAndBetaID {
				if want := []int{1, 2}; !equalInts(c.WorkspaceIDs, want) {
					t.Errorf("alpha+beta WorkspaceIDs in list = %v, want %v", c.WorkspaceIDs, want)
				}
			}
		}
	})

	t.Run("ListGlobal returns only applies-to-all rows", func(t *testing.T) {
		list, err := repo.ListActionCredentialsGlobal()
		if err != nil {
			t.Fatalf("list global: %v", err)
		}
		if len(list) != 1 || list[0].ID != globalID {
			t.Fatalf("want one row (the global), got %v", list)
		}
	})

	t.Run("Rotate replaces ciphertext but keeps metadata", func(t *testing.T) {
		if err := repo.RotateActionCredential(alphaOnlyID, "ciphertext-alpha-v2", "lin_…"); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		got, err := repo.GetActionCredentialByID(alphaOnlyID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.EncryptedSecret != "ciphertext-alpha-v2" {
			t.Errorf("ciphertext not rotated: %q", got.EncryptedSecret)
		}
		if got.SecretPrefix != "lin_…" {
			t.Errorf("prefix not updated: %q", got.SecretPrefix)
		}
		if got.SecretMetadata != `{"provider":"linear"}` {
			t.Errorf("metadata clobbered: %q", got.SecretMetadata)
		}
	})

	t.Run("UpdateMetadata can change scope flag", func(t *testing.T) {
		got, _ := repo.GetActionCredentialByID(alphaOnlyID)
		got.Name = "alpha token (renamed)"
		got.IsEnabled = false
		got.AppliesToAllWorkspaces = true
		if err := repo.UpdateActionCredentialMetadata(got); err != nil {
			t.Fatalf("update metadata: %v", err)
		}
		reloaded, _ := repo.GetActionCredentialByID(alphaOnlyID)
		if reloaded.Name != "alpha token (renamed)" || reloaded.IsEnabled || !reloaded.AppliesToAllWorkspaces {
			t.Errorf("metadata not applied: %+v", reloaded)
		}
		if reloaded.EncryptedSecret != "ciphertext-alpha-v2" {
			t.Errorf("ciphertext changed unexpectedly: %q", reloaded.EncryptedSecret)
		}
		// Restore for later subtests.
		reloaded.AppliesToAllWorkspaces = false
		_ = repo.UpdateActionCredentialMetadata(reloaded)
	})

	t.Run("SetCredentialWorkspaces replaces the allowlist", func(t *testing.T) {
		if err := repo.SetCredentialWorkspaces(alphaAndBetaID, []int{2, 3}); err != nil {
			t.Fatalf("set workspaces: %v", err)
		}
		got, _ := repo.GetActionCredentialByID(alphaAndBetaID)
		if want := []int{2, 3}; !equalInts(got.WorkspaceIDs, want) {
			t.Errorf("WorkspaceIDs after replace = %v, want %v", got.WorkspaceIDs, want)
		}
	})

	t.Run("GetByID returns ErrNotFound for missing row", func(t *testing.T) {
		_, err := repo.GetActionCredentialByID(99999)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("Delete cascades the workspace allowlist", func(t *testing.T) {
		if err := repo.DeleteActionCredential(alphaAndBetaID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := repo.GetActionCredentialByID(alphaAndBetaID); !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
		// FK ON DELETE CASCADE should have removed join rows too.
		ids, err := repo.GetCredentialWorkspaceIDs(alphaAndBetaID)
		if err != nil {
			t.Fatalf("get workspace ids after delete: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("expected join table cleared, got %v", ids)
		}
	})
}

func TestSanitizeStripsCiphertext(t *testing.T) {
	c := &models.ActionCredential{
		ID:                     7,
		Name:                   "x",
		CredentialType:         models.CredentialBearerToken,
		AppliesToAllWorkspaces: true,
		EncryptedSecret:        "should-never-leak",
		SecretPrefix:           "tok_…",
		IsEnabled:              true,
	}
	s := c.Sanitize()
	if !s.HasSecret {
		t.Errorf("HasSecret should be true")
	}
	// Compile-time guarantee: ActionCredentialSanitized has no EncryptedSecret field.
	if s.SecretPrefix != "tok_…" {
		t.Errorf("prefix lost in sanitize")
	}
	if !s.AppliesToAllWorkspaces {
		t.Errorf("AppliesToAllWorkspaces lost in sanitize")
	}
}

func TestSecretPrefixFor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"short", ""}, // 5 chars, below 2*prefixLen threshold → fully masked
		{"ghp_AbCdEfGhIj", "ghp_…"},
	}
	for _, tc := range cases {
		got := models.SecretPrefixFor(tc.in)
		if got != tc.want {
			t.Errorf("SecretPrefixFor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]int(nil), a...)
	bb := append([]int(nil), b...)
	sort.Ints(aa)
	sort.Ints(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}
