package repository

import (
	"database/sql"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

type scmConnectionReadCountingDB struct {
	database.Database
	reads int
}

func (db *scmConnectionReadCountingDB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.reads++
	return db.Database.Query(query, args...)
}

func TestListConnectionsForWorkspacesUsesOneScopedRead(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "scm-connections-bulk.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId for %s: %v", label, err)
		}
		return int(id)
	}
	providerID := insertID(
		"provider",
		`INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled) VALUES ('bulk-scm', 'Bulk SCM', 'gitea', 'pat', true)`,
	)
	workspaceOne := insertID("workspace one", `INSERT INTO workspaces (name, key) VALUES ('SCM One', 'SC1')`)
	workspaceTwo := insertID("workspace two", `INSERT INTO workspaces (name, key) VALUES ('SCM Two', 'SC2')`)
	workspaceHidden := insertID("hidden workspace", `INSERT INTO workspaces (name, key) VALUES ('SCM Hidden', 'SCH')`)
	connectionOne := insertID(
		"connection one",
		`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled) VALUES (?, ?, true)`,
		workspaceOne, providerID,
	)
	connectionTwo := insertID(
		"connection two",
		`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled) VALUES (?, ?, true)`,
		workspaceTwo, providerID,
	)
	insertID(
		"hidden connection",
		`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled) VALUES (?, ?, true)`,
		workspaceHidden, providerID,
	)

	countingDB := &scmConnectionReadCountingDB{Database: db}
	connections, err := NewSCMWorkspaceRepository(countingDB).ListConnectionsForWorkspaces([]int{workspaceOne, workspaceTwo})
	if err != nil {
		t.Fatalf("ListConnectionsForWorkspaces: %v", err)
	}
	if countingDB.reads != 1 {
		t.Fatalf("read queries = %d, want 1 independent of workspace count", countingDB.reads)
	}
	if len(connections) != 2 || connections[0].ID != connectionOne || connections[1].ID != connectionTwo {
		t.Fatalf("connections = %+v, want %d and %d", connections, connectionOne, connectionTwo)
	}
	if connections[0].WorkspaceName != "SCM One" || connections[1].WorkspaceName != "SCM Two" {
		t.Fatalf("workspace names = %q/%q", connections[0].WorkspaceName, connections[1].WorkspaceName)
	}
}

func TestSCMConnectionEnrichmentsUseBoundedWorkspaceReads(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "scm-connection-enrichments.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId for %s: %v", label, err)
		}
		return int(id)
	}
	userID := insertID("user", `INSERT INTO users (email, username, first_name, last_name) VALUES ('scm@example.test', 'scm-user', 'SCM', 'User')`)
	workspaceID := insertID("workspace", `INSERT INTO workspaces (name, key) VALUES ('SCM Overview', 'SCO')`)
	oauthProviderID := insertID(
		"OAuth provider",
		`INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled) VALUES ('oauth-scm', 'OAuth SCM', 'gitea', 'oauth', true)`,
	)
	patProviderID := insertID(
		"PAT provider",
		`INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled) VALUES ('pat-scm', 'PAT SCM', 'gitea', 'pat', true)`,
	)
	oauthConnectionID := insertID(
		"OAuth connection",
		`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled) VALUES (?, ?, true)`,
		workspaceID, oauthProviderID,
	)
	patConnectionID := insertID(
		"PAT connection",
		`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled, personal_access_token_encrypted) VALUES (?, ?, true, 'workspace-pat')`,
		workspaceID, patProviderID,
	)
	if _, err := db.ExecWrite(`
		INSERT INTO user_scm_oauth_tokens (user_id, scm_provider_id, oauth_access_token_encrypted, scm_username)
		VALUES (?, ?, 'user-oauth', 'octocat')
	`, userID, oauthProviderID); err != nil {
		t.Fatalf("insert user OAuth token: %v", err)
	}
	for _, linkedRepository := range []struct {
		connectionID int
		externalID   string
		name         string
	}{
		{oauthConnectionID, "one", "org/one"},
		{patConnectionID, "two", "org/two"},
	} {
		if _, err := db.ExecWrite(`
			INSERT INTO workspace_repositories (
				workspace_scm_connection_id, repository_external_id, repository_name, repository_url
			) VALUES (?, ?, ?, 'https://example.test/repo')
		`, linkedRepository.connectionID, linkedRepository.externalID, linkedRepository.name); err != nil {
			t.Fatalf("insert linked repository %q: %v", linkedRepository.name, err)
		}
	}

	countingDB := &scmConnectionReadCountingDB{Database: db}
	repo := NewSCMWorkspaceRepository(countingDB)
	repositories, err := repo.ListLinkedRepositoriesForWorkspace(workspaceID)
	if err != nil {
		t.Fatalf("ListLinkedRepositoriesForWorkspace: %v", err)
	}
	if countingDB.reads != 1 || len(repositories) != 2 {
		t.Fatalf("repository reads/results = %d/%d, want 1/2", countingDB.reads, len(repositories))
	}

	countingDB.reads = 0
	statuses, err := repo.ListConnectionAuthStatusesForWorkspace(workspaceID, userID)
	if err != nil {
		t.Fatalf("ListConnectionAuthStatusesForWorkspace: %v", err)
	}
	if countingDB.reads != 1 {
		t.Fatalf("auth status reads = %d, want 1 independent of connection count", countingDB.reads)
	}
	if oauth := statuses[oauthConnectionID]; oauth == nil || !oauth.HasUserToken || oauth.HasWorkspaceToken || !oauth.IsAuthenticated || oauth.SCMUsername != "octocat" {
		t.Fatalf("OAuth status = %+v, want authenticated user token", oauth)
	}
	if pat := statuses[patConnectionID]; pat == nil || !pat.HasWorkspacePAT || !pat.IsAuthenticated {
		t.Fatalf("PAT status = %+v, want authenticated workspace PAT", pat)
	}
}
