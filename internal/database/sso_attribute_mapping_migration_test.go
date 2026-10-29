//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

const (
	ssoEmailVerifiedMappingMigration = "20260831_sso_email_verified_attribute_mapping"
	legacySSOAttributeMapping        = `{"email":"email","name":"name","given_name":"given_name","family_name":"family_name","username":"preferred_username"}`
	currentSSOAttributeMapping       = `{"email":"email","name":"name","given_name":"given_name","family_name":"family_name","username":"preferred_username","email_verified":"email_verified"}`
)

// The fixture uses the pre-migration table definition because the canonical
// schema now creates attribute_mapping with the new default.
func seedLegacySSOProviderSchema(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()

	providerSchema := `CREATE TABLE sso_providers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		slug TEXT UNIQUE NOT NULL,
		name TEXT NOT NULL,
		provider_type TEXT NOT NULL DEFAULT 'oidc',
		enabled BOOLEAN DEFAULT FALSE,
		is_default BOOLEAN DEFAULT FALSE,
		issuer_url TEXT,
		client_id TEXT,
		client_secret_encrypted TEXT,
		scopes TEXT DEFAULT 'openid email profile',
		auto_provision_users BOOLEAN DEFAULT FALSE,
		require_verified_email BOOLEAN DEFAULT TRUE,
		attribute_mapping TEXT DEFAULT '` + legacySSOAttributeMapping + `',
		saml_idp_metadata_url TEXT,
		saml_idp_sso_url TEXT,
		saml_idp_certificate TEXT,
		saml_sp_entity_id TEXT,
		saml_sign_requests BOOLEAN DEFAULT FALSE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`
	stateSchema := `CREATE TABLE sso_state_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider_id INTEGER NOT NULL REFERENCES sso_providers(id) ON DELETE CASCADE
	)`
	externalAccountSchema := `CREATE TABLE user_external_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		provider_id INTEGER NOT NULL REFERENCES sso_providers(id) ON DELETE CASCADE
	)`
	if testutils.IsPostgres() {
		providerSchema = `CREATE TABLE sso_providers (
			id SERIAL PRIMARY KEY,
			slug TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			provider_type TEXT NOT NULL DEFAULT 'oidc',
			enabled BOOLEAN DEFAULT FALSE,
			is_default BOOLEAN DEFAULT FALSE,
			issuer_url TEXT,
			client_id TEXT,
			client_secret_encrypted TEXT,
			scopes TEXT DEFAULT 'openid email profile',
			auto_provision_users BOOLEAN DEFAULT FALSE,
			require_verified_email BOOLEAN DEFAULT TRUE,
			attribute_mapping TEXT DEFAULT '` + legacySSOAttributeMapping + `',
			saml_idp_metadata_url TEXT,
			saml_idp_sso_url TEXT,
			saml_idp_certificate TEXT,
			saml_sp_entity_id TEXT,
			saml_sign_requests BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`
		stateSchema = `CREATE TABLE sso_state_tokens (
			id SERIAL PRIMARY KEY,
			provider_id INTEGER NOT NULL REFERENCES sso_providers(id) ON DELETE CASCADE
		)`
		externalAccountSchema = `CREATE TABLE user_external_accounts (
			id SERIAL PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			provider_id INTEGER NOT NULL REFERENCES sso_providers(id) ON DELETE CASCADE
		)`
	}

	statements := []string{
		providerSchema,
		`CREATE INDEX idx_sso_providers_slug ON sso_providers(slug)`,
		`CREATE INDEX idx_sso_providers_enabled ON sso_providers(enabled)`,
		`CREATE INDEX idx_sso_providers_default ON sso_providers(is_default)`,
		stateSchema,
		externalAccountSchema,
		`INSERT INTO users (id) VALUES (1)`,
		`INSERT INTO sso_providers (id, slug, name, enabled, attribute_mapping)
			VALUES (7, 'legacy', 'Legacy Provider', true, '` + legacySSOAttributeMapping + `')`,
		`INSERT INTO sso_state_tokens (id, provider_id) VALUES (11, 7)`,
		`INSERT INTO user_external_accounts (id, user_id, provider_id) VALUES (13, 1, 7)`,
	}
	for _, statement := range statements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed legacy SSO schema with %q: %v", statement, err)
		}
	}
}

func TestSSOEmailVerifiedAttributeMappingMigrationUpdatesDefault(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	workspaceSchema := `CREATE TABLE workspaces (id INTEGER PRIMARY KEY AUTOINCREMENT)`
	if testutils.IsPostgres() {
		workspaceSchema = `CREATE TABLE workspaces (id SERIAL PRIMARY KEY)`
	}
	if _, err := tdb.Exec(workspaceSchema); err != nil {
		t.Fatalf("seed workspaces probe table: %v", err)
	}
	seedLegacySSOProviderSchema(t, tdb)
	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, ssoEmailVerifiedMappingMigration, "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run SSO attribute mapping migration: %v", err)
	}

	var mapping string
	if _, err := tdb.Exec(`INSERT INTO sso_providers (id, slug, name) VALUES (8, 'new', 'New Provider')`); err != nil {
		t.Fatalf("insert provider using migrated default: %v", err)
	}
	if err := tdb.QueryRow(`SELECT attribute_mapping FROM sso_providers WHERE id=8`).Scan(&mapping); err != nil {
		t.Fatalf("read migrated attribute mapping default: %v", err)
	}
	if mapping != currentSSOAttributeMapping {
		t.Fatalf("attribute_mapping = %q, want %q", mapping, currentSSOAttributeMapping)
	}

	var providers, states, externalAccounts int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM sso_providers WHERE id=7 AND slug='legacy' AND attribute_mapping=?`, legacySSOAttributeMapping).Scan(&providers); err != nil {
		t.Fatalf("check legacy provider: %v", err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM sso_state_tokens WHERE id=11 AND provider_id=7`).Scan(&states); err != nil {
		t.Fatalf("check SSO state token: %v", err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM user_external_accounts WHERE id=13 AND user_id=1 AND provider_id=7`).Scan(&externalAccounts); err != nil {
		t.Fatalf("check external account: %v", err)
	}
	if providers != 1 || states != 1 || externalAccounts != 1 {
		t.Fatalf("preserved rows = providers:%d states:%d external_accounts:%d, want 1 each", providers, states, externalAccounts)
	}

	if _, err := tdb.Exec(`INSERT INTO sso_state_tokens (id, provider_id) VALUES (12, 9999)`); err == nil {
		t.Fatal("foreign key enforcement was not restored after migration")
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	var stamps int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, ssoEmailVerifiedMappingMigration).Scan(&stamps); err != nil || stamps != 1 {
		t.Fatalf("SSO attribute mapping migration stamps = %d, %v; want 1", stamps, err)
	}
}
