//go:build test

package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"windshift/internal/models"
	"windshift/internal/scm"
	"windshift/internal/services"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

func TestOpenPRViaCredentialResolverAuthenticatesWithGitHubApp(t *testing.T) {
	const installationToken = "installation-token"
	var tokenMinted atomic.Bool
	var prAuthorized atomic.Bool

	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") == "" {
				http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
				return
			}
			tokenMinted.Store(true)
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"token":"`+installationToken+`","expires_at":"2099-01-01T00:00:00Z"}`)
		case "/repos/Windshiftapp/core/pulls":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+installationToken {
				http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
				return
			}
			prAuthorized.Store(true)
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"id":9001,"number":12,"title":"Fix GitHub App PR auth","state":"open","html_url":"https://github.example/Windshiftapp/core/pull/12","user":{"login":"windshift-app[bot]"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(github.Close)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	privateKeyPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}))

	db := testutils.CreateTestDB(t, true)
	if db.Engine == "sqlite" {
		t.Cleanup(func() { _ = db.Close() })
	}
	encryption := sso.NewSecretEncryption("github-app-pr-test-secret-with-enough-length")
	encryptedKey, err := encryption.Encrypt(privateKeyPEM)
	if err != nil {
		t.Fatalf("encrypt private key: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key) VALUES (1, 'Windshift', 'WI')`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO scm_providers(
			id, slug, name, provider_type, auth_method, base_url, enabled,
			github_app_id, github_app_private_key_encrypted, github_app_installation_id
		) VALUES (1, 'github-app', 'GitHub App', ?, ?, ?, true, '1234', ?, '42')`,
		string(models.SCMProviderTypeGitHub), string(models.SCMAuthMethodGitHubApp), github.URL, encryptedKey); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_scm_connections(id, workspace_id, scm_provider_id, enabled)
		VALUES (7, 1, 1, true)`); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	resolver := scm.NewCredentialResolver(db, encryption)
	opened, err := openPRViaCredentialResolver(resolver)(t.Context(), services.OpenPRRequest{
		ConnectionID: 7,
		Owner:        "Windshiftapp",
		Repo:         "core",
		HeadBranch:   "agent-runs/run-12",
		BaseBranch:   "main",
		Title:        "Fix GitHub App PR auth",
	})
	if err != nil {
		t.Fatalf("open PR: %v", err)
	}
	if !tokenMinted.Load() {
		t.Fatal("GitHub App installation token was not minted")
	}
	if !prAuthorized.Load() {
		t.Fatal("pull request request did not use the installation token")
	}
	if opened.Number != 12 || opened.ID != "9001" || opened.Author != "windshift-app[bot]" {
		t.Fatalf("opened PR = %+v", opened)
	}
}
