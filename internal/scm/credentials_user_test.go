//go:build test

package scm

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

// WI-275 companions: GetCredentialsForUser is the credential resolution
// behind per-user agent-run SCM access. These pin the PAT fallback
// hierarchy (workspace PAT preferred over provider PAT — same as
// GetCredentialsByConnectionID, which previously differed) and the
// no-fallback OAuth contract (ErrUserSCMNotConnected).

func seedUserCredsFixture(t *testing.T, authMethod string, wsPAT, providerPAT string) (*CredentialResolver, *testutils.TestDB) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	enc := sso.NewSecretEncryption("user-creds-test-secret-with-enough-length-for-hkdf")

	encOrNil := func(v string) any {
		if v == "" {
			return nil
		}
		out, err := enc.Encrypt(v)
		if err != nil {
			t.Fatalf("encrypt %q: %v", v, err)
		}
		return out
	}

	if _, err := tdb.Exec(`INSERT INTO workspaces(id, name, key) VALUES (1, 'WS', 'WS')`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO scm_providers(id, slug, name, provider_type, auth_method, enabled, personal_access_token_encrypted)
		VALUES (1, 'uc-test', 'Gitea', ?, ?, true, ?)`,
		string(models.SCMProviderTypeGitea), authMethod, encOrNil(providerPAT)); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO workspace_scm_connections(id, workspace_id, scm_provider_id, enabled, personal_access_token_encrypted)
		VALUES (9, 1, 1, true, ?)`, encOrNil(wsPAT)); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	return NewCredentialResolver(tdb, enc), tdb
}

func TestGetCredentialsForUser_PATPrefersWorkspaceOverProvider(t *testing.T) {
	resolver, _ := seedUserCredsFixture(t, string(models.SCMAuthMethodPAT), "ws-pat", "provider-pat")

	creds, err := resolver.GetCredentialsForUser(context.Background(), 9, 42)
	if err != nil {
		t.Fatalf("GetCredentialsForUser: %v", err)
	}
	if creds.PersonalAccessToken != "ws-pat" {
		t.Errorf("PAT: want workspace-level ws-pat, got %q", creds.PersonalAccessToken)
	}
	if creds.AuthSource != "workspace" {
		t.Errorf("auth source: want workspace, got %q", creds.AuthSource)
	}
}

func TestGetCredentialsForUser_PATFallsBackToProvider(t *testing.T) {
	resolver, _ := seedUserCredsFixture(t, string(models.SCMAuthMethodPAT), "", "provider-pat")

	creds, err := resolver.GetCredentialsForUser(context.Background(), 9, 42)
	if err != nil {
		t.Fatalf("GetCredentialsForUser: %v", err)
	}
	if creds.PersonalAccessToken != "provider-pat" {
		t.Errorf("PAT: want provider-pat, got %q", creds.PersonalAccessToken)
	}
	if creds.AuthSource != "provider" {
		t.Errorf("auth source: want provider, got %q", creds.AuthSource)
	}
}

func TestGetCredentialsForUser_OAuthWithoutUserTokenIsNotConnected(t *testing.T) {
	resolver, _ := seedUserCredsFixture(t, string(models.SCMAuthMethodOAuth), "", "")

	_, err := resolver.GetCredentialsForUser(context.Background(), 9, 42)
	if !errors.Is(err, ErrUserSCMNotConnected) {
		t.Fatalf("expected ErrUserSCMNotConnected (no fallback for OAuth), got %v", err)
	}
}
