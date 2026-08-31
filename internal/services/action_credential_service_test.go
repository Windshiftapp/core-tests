package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

const testServerSecret = "test-server-secret-with-sufficient-length-for-derivation"

func newTestCredentialService(t *testing.T) (*ActionCredentialService, database.Database) {
	t.Helper()
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
		t.Fatalf("seed: %v", err)
	}
	repo := repository.NewActionCredentialRepository(db)
	return NewActionCredentialService(repo, testServerSecret), db
}

func TestActionCredentialService_CreateEncryptsAndStripsPlaintext(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	created, err := svc.Create(models.CreateActionCredentialRequest{
		Name:           "GitHub PAT",
		CredentialType: models.CredentialBearerToken,
		Secret:         "ghp_example-test-token",
	}, ptrInt(10))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.EncryptedSecret == "" {
		t.Fatalf("encrypted_secret empty after Create")
	}
	if strings.Contains(created.EncryptedSecret, "ghp_") {
		t.Fatalf("ciphertext contains plaintext fragment: %q", created.EncryptedSecret)
	}
	if created.SecretPrefix != "ghp_…" {
		t.Errorf("SecretPrefix = %q, want %q", created.SecretPrefix, "ghp_…")
	}
	if !created.AppliesToAllWorkspaces {
		t.Errorf("defaults to applies-to-all when scope not specified")
	}

	// Sanitized form must not have any way to recover plaintext or ciphertext.
	sanitized := created.Sanitize()
	if !sanitized.HasSecret {
		t.Errorf("HasSecret should be true")
	}
}

func TestActionCredentialService_CreateScoped(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	appliesAll := false
	created, err := svc.Create(models.CreateActionCredentialRequest{
		Name:                   "alpha+beta",
		CredentialType:         models.CredentialBearerToken,
		Secret:                 "scoped-secret-1234567890",
		AppliesToAllWorkspaces: &appliesAll,
		WorkspaceIDs:           []int{1, 2, 1}, // dedupe should drop the repeat
	}, ptrInt(10))
	if err != nil {
		t.Fatalf("create scoped: %v", err)
	}
	if created.AppliesToAllWorkspaces {
		t.Errorf("AppliesToAllWorkspaces should be false")
	}
	if len(created.WorkspaceIDs) != 2 {
		t.Errorf("WorkspaceIDs = %v, want 2 deduped IDs", created.WorkspaceIDs)
	}
}

func TestActionCredentialService_CreateScopedRequiresWorkspaceIDs(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	appliesAll := false
	_, err := svc.Create(models.CreateActionCredentialRequest{
		Name:                   "missing-ids",
		CredentialType:         models.CredentialBearerToken,
		Secret:                 "secret-value-1234567890",
		AppliesToAllWorkspaces: &appliesAll,
	}, ptrInt(10))
	if err == nil {
		t.Fatalf("expected error when scope is false and workspace_ids is empty")
	}
}

func TestActionCredentialService_ResolveDecrypts(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	const plaintext = "ghp_example-test-token"
	created, err := svc.Create(models.CreateActionCredentialRequest{
		Name:           "GitHub PAT",
		CredentialType: models.CredentialBearerToken,
		Secret:         plaintext,
	}, ptrInt(10))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, _, err := svc.Resolve(context.Background(), created.ID, 1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != plaintext {
		t.Errorf("resolve plaintext mismatch: got %q want %q", got, plaintext)
	}
}

func TestActionCredentialService_Resolve_ScopeMismatch(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	appliesAll := false
	created, err := svc.Create(models.CreateActionCredentialRequest{
		Name:                   "alpha-only",
		CredentialType:         models.CredentialBearerToken,
		Secret:                 "alpha-secret-1234567890",
		AppliesToAllWorkspaces: &appliesAll,
		WorkspaceIDs:           []int{1},
	}, ptrInt(10))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Same workspace works.
	if _, _, err := svc.Resolve(context.Background(), created.ID, 1); err != nil {
		t.Errorf("same-workspace resolve failed: %v", err)
	}
	// Other workspace must be blocked.
	if _, _, err := svc.Resolve(context.Background(), created.ID, 2); !errors.Is(err, ErrCredentialScopeMismatch) {
		t.Errorf("other-workspace resolve: want ErrCredentialScopeMismatch, got %v", err)
	}
}

func TestActionCredentialService_Resolve_Disabled(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	disabled := false
	created, err := svc.Create(models.CreateActionCredentialRequest{
		Name:           "disabled",
		CredentialType: models.CredentialBearerToken,
		Secret:         "secret-value-1234567890",
		IsEnabled:      &disabled,
	}, ptrInt(10))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := svc.Resolve(context.Background(), created.ID, 1); !errors.Is(err, ErrCredentialDisabled) {
		t.Errorf("want ErrCredentialDisabled, got %v", err)
	}
}

func TestActionCredentialService_Rotate(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	created, err := svc.Create(models.CreateActionCredentialRequest{
		Name:           "rotate-me",
		CredentialType: models.CredentialBearerToken,
		Secret:         "old-secret-1234567890",
	}, ptrInt(10))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Rotate(created.ID, models.RotateActionCredentialRequest{
		Secret: "new-secret-9876543210",
	}); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	plaintext, _, err := svc.Resolve(context.Background(), created.ID, 1)
	if err != nil {
		t.Fatalf("resolve after rotate: %v", err)
	}
	if plaintext != "new-secret-9876543210" {
		t.Errorf("rotated plaintext mismatch: got %q", plaintext)
	}
}

func TestActionCredentialService_UpdateMetadata_ScopeTransitions(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	created, err := svc.Create(models.CreateActionCredentialRequest{
		Name:           "transition",
		CredentialType: models.CredentialBearerToken,
		Secret:         "scoped-secret-1234567890",
	}, ptrInt(10))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Flip from applies-to-all to scoped(1,2). Both workspaces should resolve.
	appliesAll := false
	wsIDs := []int{1, 2}
	if _, err := svc.UpdateMetadata(created.ID, models.UpdateActionCredentialRequest{
		AppliesToAllWorkspaces: &appliesAll,
		WorkspaceIDs:           &wsIDs,
	}); err != nil {
		t.Fatalf("update to scoped: %v", err)
	}
	if _, _, err := svc.Resolve(context.Background(), created.ID, 1); err != nil {
		t.Errorf("ws1 resolve after scope change: %v", err)
	}
	if _, _, err := svc.Resolve(context.Background(), created.ID, 3); !errors.Is(err, ErrCredentialScopeMismatch) {
		t.Errorf("ws3 should be blocked, got %v", err)
	}

	// Flip back to applies-to-all. Every workspace should resolve again.
	appliesAll2 := true
	if _, err := svc.UpdateMetadata(created.ID, models.UpdateActionCredentialRequest{
		AppliesToAllWorkspaces: &appliesAll2,
	}); err != nil {
		t.Fatalf("update to applies-all: %v", err)
	}
	if _, _, err := svc.Resolve(context.Background(), created.ID, 3); err != nil {
		t.Errorf("ws3 resolve after flip back to applies-all: %v", err)
	}
}

func TestActionCredentialService_UpdateMetadata_ScopedRequiresIDs(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	created, _ := svc.Create(models.CreateActionCredentialRequest{
		Name:           "needs-ids",
		CredentialType: models.CredentialBearerToken,
		Secret:         "secret-value-1234567890",
	}, ptrInt(10))

	appliesAll := false
	empty := []int{}
	if _, err := svc.UpdateMetadata(created.ID, models.UpdateActionCredentialRequest{
		AppliesToAllWorkspaces: &appliesAll,
		WorkspaceIDs:           &empty,
	}); err == nil {
		t.Fatalf("expected error when flipping scoped with empty workspace_ids")
	}
}

func TestActionCredentialService_RejectsInvalidType(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	_, err := svc.Create(models.CreateActionCredentialRequest{
		Name:           "x",
		CredentialType: models.ActionCredentialType("malformed_type"),
		Secret:         "anything",
	}, nil)
	if err == nil {
		t.Fatalf("expected error for invalid type")
	}
}

func TestActionCredentialService_RejectsSensitiveMetadataKeys(t *testing.T) {
	svc, _ := newTestCredentialService(t)
	for _, key := range []string{"secret", "token", "password", "client_secret", "api_token"} {
		_, err := svc.Create(models.CreateActionCredentialRequest{
			Name:           "bad-" + key,
			CredentialType: models.CredentialBearerToken,
			Secret:         "real-secret-1234567890",
			SecretMetadata: `{"` + key + `":"leak"}`,
		}, nil)
		if err == nil {
			t.Errorf("metadata key %q must be rejected", key)
		}
	}
}

func TestCanCapabilityReference(t *testing.T) {
	global := &models.ActionCredential{AppliesToAllWorkspaces: true}
	scopedAlpha := &models.ActionCredential{WorkspaceIDs: []int{1}}
	scopedAlphaBeta := &models.ActionCredential{WorkspaceIDs: []int{1, 2}}

	// Global credential is always usable.
	if !CanCapabilityReference(global, nil) {
		t.Error("applies-to-all capability should reference applies-to-all credential")
	}
	if !CanCapabilityReference(global, []int{2, 3}) {
		t.Error("scoped capability should always be able to reference applies-to-all credential")
	}

	// Capability that applies-to-all needs a credential that applies-to-all.
	if CanCapabilityReference(scopedAlpha, nil) {
		t.Error("applies-to-all capability must NOT reference workspace-restricted credential")
	}

	// Superset semantics: every workspace the capability runs in must be in
	// the credential's allowlist.
	if CanCapabilityReference(scopedAlpha, []int{1, 2}) {
		t.Error("alpha-only credential must NOT be allowed for capability covering {1,2}")
	}
	if !CanCapabilityReference(scopedAlphaBeta, []int{1, 2}) {
		t.Error("{1,2} credential should be allowed for capability covering {1,2}")
	}
	if !CanCapabilityReference(scopedAlphaBeta, []int{1}) {
		t.Error("{1,2} credential should be allowed for capability covering {1}")
	}
	if CanCapabilityReference(scopedAlphaBeta, []int{1, 3}) {
		t.Error("{1,2} credential must NOT cover capability covering {1,3}")
	}
}

func ptrInt(v int) *int { return &v }

func TestScanLegacyInlineSecrets(t *testing.T) {
	db, err := database.NewSQLiteDB("file::memory:?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE action_capabilities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			capability_type TEXT NOT NULL,
			config TEXT NOT NULL
		);
		INSERT INTO action_capabilities (name, capability_type, config) VALUES
			('clean', 'http_client', '{"allowed_url_patterns":["https://x"],"default_headers":{"Accept":"application/json"},"timeout_secs":30}'),
			('dirty', 'http_client', '{"allowed_url_patterns":["https://y"],"default_headers":{"Authorization":"Bearer LEAK","X-API-Key":"k1"},"timeout_secs":30}'),
			('docker', 'docker_environment', '{"image":"alpine"}');
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits := ScanLegacyInlineSecrets(db)
	if hits != 2 {
		t.Fatalf("want 2 sensitive headers detected, got %d", hits)
	}
}
