package services

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

type trackingWorkspaceKeyInvalidator struct {
	calls int
}

func (i *trackingWorkspaceKeyInvalidator) Invalidate() error {
	i.calls++
	return nil
}

func newOffboardEnv(t *testing.T) (database.Database, int) {
	t.Helper()

	dsn := fmt.Sprintf("file:offboard-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	res, err := db.Exec(`INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, ?, '')`,
		"hank@example.com", "hank", "Hank")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	uid64, _ := res.LastInsertId()
	return db, int(uid64)
}

func insertAPIToken(t *testing.T, db database.Database, userID int, name string) int {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO api_tokens (user_id, name, token_hash, token_prefix, permissions)
		VALUES (?, ?, ?, ?, '["read"]')
	`, userID, name, "hash-"+name, "crw_"+name)
	if err != nil {
		t.Fatalf("insert api_token %s: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

func TestOffboardUser_RevokesAPITokensAndReturnsIDs(t *testing.T) {
	db, uid := newOffboardEnv(t)

	t1 := insertAPIToken(t, db, uid, "tok1")
	t2 := insertAPIToken(t, db, uid, "tok2")

	result, err := OffboardUser(db, uid, nil)
	if err != nil {
		t.Fatalf("OffboardUser: %v", err)
	}

	got := append([]int(nil), result.RevokedAPITokenIDs...)
	sort.Ints(got)
	want := []int{t1, t2}
	sort.Ints(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("revoked IDs mismatch: got %v, want %v", got, want)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, uid).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected 0 api_tokens for offboarded user, got %d", remaining)
	}
}

func TestOffboardUser_NoTokens_ReturnsEmptySlice(t *testing.T) {
	db, uid := newOffboardEnv(t)

	result, err := OffboardUser(db, uid, nil)
	if err != nil {
		t.Fatalf("OffboardUser: %v", err)
	}
	if len(result.RevokedAPITokenIDs) != 0 {
		t.Fatalf("expected empty slice, got %v", result.RevokedAPITokenIDs)
	}
}

func TestOffboardUserInvalidatesImplicitAndWorkspaceCaches(t *testing.T) {
	db, offboardedUserID := newOffboardEnv(t)
	observerResult, err := db.Exec(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('observer@example.test', 'observer', 'Other', 'User', true)
	`)
	if err != nil {
		t.Fatalf("insert observer: %v", err)
	}
	observerID64, err := observerResult.LastInsertId()
	if err != nil {
		t.Fatalf("observer LastInsertId: %v", err)
	}
	observerID := int(observerID64)
	workspaceResult, err := db.Exec(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Offboard', 'OFF', '', true, false)
	`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID64, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace LastInsertId: %v", err)
	}
	workspaceID := int(workspaceID64)
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		SELECT ?, ?, id FROM workspace_roles WHERE builtin_key = ?
	`, offboardedUserID, workspaceID, models.RoleBuiltinViewer); err != nil {
		t.Fatalf("assign last Viewer: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (name, key, description, active, is_personal, owner_id)
		VALUES ('Personal', 'PERSONAL', '', true, true, ?)
	`, offboardedUserID); err != nil {
		t.Fatalf("insert personal workspace: %v", err)
	}

	config := DefaultPermissionCacheConfig()
	config.TTL = time.Minute
	config.WarmupOnStartup = false
	permissionService, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	allowed, err := permissionService.HasWorkspacePermission(observerID, workspaceID, models.PermissionItemView)
	if err != nil {
		t.Fatalf("warm observer permission: %v", err)
	}
	if allowed {
		t.Fatal("observer unexpectedly had Viewer while the role was restricted")
	}

	keyInvalidator := &trackingWorkspaceKeyInvalidator{}
	invalidator := NewAuthorizationCacheInvalidator(permissionService, keyInvalidator)
	if _, err := OffboardUser(db, offboardedUserID, nil, invalidator); err != nil {
		t.Fatalf("OffboardUser: %v", err)
	}
	allowed, err = permissionService.HasWorkspacePermission(observerID, workspaceID, models.PermissionItemView)
	if err != nil {
		t.Fatalf("observer permission after offboarding: %v", err)
	}
	if !allowed {
		t.Fatal("last Viewer removal did not reopen implicit Viewer access")
	}
	if keyInvalidator.calls != 1 {
		t.Fatalf("workspace key invalidations = %d, want 1", keyInvalidator.calls)
	}
}

func offboardExec(t *testing.T, db database.Database, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func offboardCount(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestOffboardUser_MarksOffboardedAndDeletesPendingInvitations(t *testing.T) {
	db, uid := newOffboardEnv(t)

	offboardExec(t, db, `
		INSERT INTO user_invitations (user_id, token, expires_at, created_at)
		VALUES (?, 'pending-token', datetime('now', '+7 days'), CURRENT_TIMESTAMP)
	`, uid)

	if _, err := OffboardUser(db, uid, nil); err != nil {
		t.Fatalf("OffboardUser: %v", err)
	}

	if n := offboardCount(t, db, `SELECT COUNT(*) FROM user_invitations WHERE user_id = ?`, uid); n != 0 {
		t.Fatalf("pending invitations survived offboarding: %d", n)
	}
	var offboardedAt *time.Time
	if err := db.QueryRow(`SELECT offboarded_at FROM users WHERE id = ?`, uid).Scan(&offboardedAt); err != nil {
		t.Fatalf("read offboarded_at: %v", err)
	}
	if offboardedAt == nil {
		t.Fatal("offboarded_at was not set")
	}
}

func TestOffboardUser_RemovesUserOwnedSecretsAndSyncState(t *testing.T) {
	db, uid := newOffboardEnv(t)
	uidText := strconv.Itoa(uid)

	offboardExec(t, db, `
		INSERT INTO integration_providers (id, slug, name, provider_type, oauth_client_id, oauth_client_secret_encrypted)
		VALUES ('prov-todoist', 'todoist-main', 'Todoist', 'todoist', 'client-id', 'enc-secret')
	`)
	offboardExec(t, db, `
		INSERT INTO user_integration_tokens (id, user_id, integration_provider_id, oauth_access_token_encrypted)
		VALUES ('uit-1', ?, 'prov-todoist', 'enc-todoist-token')
	`, uidText)
	offboardExec(t, db, `
		INSERT INTO integration_oauth_state (id, provider_id, state, user_id, expires_at)
		VALUES ('state-1', 'prov-todoist', 'state-1-value', ?, datetime('now', '+5 minutes'))
	`, uidText)
	offboardExec(t, db, `
		INSERT INTO todoist_sync_config (id, user_id, integration_provider_id, personal_workspace_id, enabled)
		VALUES ('tsc-1', ?, 'prov-todoist', 1, true)
	`, uidText)
	offboardExec(t, db, `
		INSERT INTO todoist_task_links (id, user_id, item_id, todoist_task_id)
		VALUES ('ttl-1', ?, 9001, 'task-1')
	`, uidText)
	offboardExec(t, db, `
		INSERT INTO calendar_feed_tokens (user_id, token) VALUES (?, 'feed-token')
	`, uid)

	if _, err := OffboardUser(db, uid, nil); err != nil {
		t.Fatalf("OffboardUser: %v", err)
	}

	// The integration tables key user_id as TEXT, the calendar table as INTEGER.
	for _, tc := range []struct {
		name  string
		query string
		arg   any
	}{
		{"user_integration_tokens", `SELECT COUNT(*) FROM user_integration_tokens WHERE user_id = ?`, uidText},
		{"integration_oauth_state", `SELECT COUNT(*) FROM integration_oauth_state WHERE user_id = ?`, uidText},
		{"todoist_sync_config", `SELECT COUNT(*) FROM todoist_sync_config WHERE user_id = ?`, uidText},
		{"todoist_task_links", `SELECT COUNT(*) FROM todoist_task_links WHERE user_id = ?`, uidText},
		{"calendar_feed_tokens", `SELECT COUNT(*) FROM calendar_feed_tokens WHERE user_id = ?`, uid},
	} {
		if n := offboardCount(t, db, tc.query, tc.arg); n != 0 {
			t.Fatalf("%s rows survived offboarding: %d", tc.name, n)
		}
	}
}

func TestOffboardUser_RemovesMembershipsSchedulesAndRoles(t *testing.T) {
	db, uid := newOffboardEnv(t)

	res, err := db.Exec(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('keeper@example.test', 'keeper', 'Keep', 'Er')
	`)
	if err != nil {
		t.Fatalf("insert keeper: %v", err)
	}
	keeperID64, _ := res.LastInsertId()
	keeperID := int(keeperID64)

	offboardExec(t, db, `INSERT INTO teams (name) VALUES ('offboard-team')`)
	teamID := offboardCount(t, db, `SELECT id FROM teams WHERE name = 'offboard-team'`)
	offboardExec(t, db, `INSERT INTO team_members (team_id, user_id) VALUES (?, ?)`, teamID, uid)
	offboardExec(t, db, `INSERT INTO on_call_schedules (team_id, name) VALUES (?, 'offboard-schedule')`, teamID)
	scheduleID := offboardCount(t, db, `SELECT id FROM on_call_schedules WHERE name = 'offboard-schedule'`)
	offboardExec(t, db, `
		INSERT INTO on_call_schedule_layers (schedule_id, name, start_date)
		VALUES (?, 'layer', '2026-01-01')
	`, scheduleID)
	layerID := offboardCount(t, db, `SELECT id FROM on_call_schedule_layers WHERE name = 'layer'`)
	offboardExec(t, db, `
		INSERT INTO on_call_schedule_layer_members (layer_id, user_id, position)
		VALUES (?, ?, 1)
	`, layerID, uid)
	offboardExec(t, db, `
		INSERT INTO on_call_schedule_overrides (schedule_id, user_id, override_user_id, start_time, end_time)
		VALUES (?, ?, ?, '2026-01-10 09:00:00', '2026-01-11 09:00:00')
	`, scheduleID, uid, keeperID)
	offboardExec(t, db, `
		INSERT INTO user_leave_periods (user_id, substitute_user_id, start_date, end_date)
		VALUES (?, ?, '2026-02-01', '2026-02-05')
	`, keeperID, uid)
	offboardExec(t, db, `INSERT INTO asset_management_sets (name) VALUES ('offboard-set')`)
	setID := offboardCount(t, db, `SELECT id FROM asset_management_sets WHERE name = 'offboard-set'`)
	offboardExec(t, db, `INSERT INTO asset_roles (name) VALUES ('offboard-role')`)
	roleID := offboardCount(t, db, `SELECT id FROM asset_roles WHERE name = 'offboard-role'`)
	offboardExec(t, db, `
		INSERT INTO user_asset_set_roles (user_id, set_id, role_id) VALUES (?, ?, ?)
	`, uid, setID, roleID)

	if _, err := OffboardUser(db, uid, nil); err != nil {
		t.Fatalf("OffboardUser: %v", err)
	}

	if n := offboardCount(t, db, `SELECT COUNT(*) FROM team_members WHERE user_id = ?`, uid); n != 0 {
		t.Fatalf("team_members rows survived offboarding: %d", n)
	}
	if n := offboardCount(t, db, `SELECT COUNT(*) FROM on_call_schedule_layer_members WHERE user_id = ?`, uid); n != 0 {
		t.Fatalf("on-call layer memberships survived offboarding: %d", n)
	}
	if n := offboardCount(t, db, `SELECT COUNT(*) FROM on_call_schedule_overrides WHERE user_id = ? OR override_user_id = ?`, uid, uid); n != 0 {
		t.Fatalf("on-call overrides survived offboarding: %d", n)
	}
	if n := offboardCount(t, db, `SELECT COUNT(*) FROM user_asset_set_roles WHERE user_id = ?`, uid); n != 0 {
		t.Fatalf("user_asset_set_roles rows survived offboarding: %d", n)
	}
	var substitute sql.NullInt64
	if err := db.QueryRow(
		`SELECT substitute_user_id FROM user_leave_periods WHERE user_id = ?`, keeperID,
	).Scan(&substitute); err != nil {
		t.Fatalf("read leave period: %v", err)
	}
	if substitute.Valid {
		t.Fatalf("offboarded substitute_user_id not released: got %d", substitute.Int64)
	}
}

func TestOffboardUser_RemovesPortalRequestDrafts(t *testing.T) {
	db, uid := newOffboardEnv(t)

	offboardExec(t, db, `INSERT INTO item_types (name) VALUES ('offboard-draft-type')`)
	itemTypeID := offboardCount(t, db, `SELECT id FROM item_types WHERE name = 'offboard-draft-type'`)
	offboardExec(t, db, `
		INSERT INTO channels (name, type, direction) VALUES ('offboard-portal', 'portal', 'inbound')
	`)
	channelID := offboardCount(t, db, `SELECT id FROM channels WHERE name = 'offboard-portal'`)
	offboardExec(t, db, `INSERT INTO workspaces (name, key) VALUES ('offboard-workspace', 'OFFW')`)
	workspaceID := offboardCount(t, db, `SELECT id FROM workspaces WHERE key = 'OFFW'`)
	offboardExec(t, db, `
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id)
		VALUES (?, 'offboard-request', ?, ?)
	`, channelID, itemTypeID, workspaceID)
	requestTypeID := offboardCount(t, db, `SELECT id FROM request_types WHERE name = 'offboard-request'`)
	offboardExec(t, db, `
		INSERT INTO portal_request_drafts (channel_id, request_type_id, user_id, title, description)
		VALUES (?, ?, ?, 'draft title', 'draft body')
	`, channelID, requestTypeID, uid)

	if _, err := OffboardUser(db, uid, nil); err != nil {
		t.Fatalf("OffboardUser: %v", err)
	}

	if n := offboardCount(t, db, `SELECT COUNT(*) FROM portal_request_drafts WHERE user_id = ?`, uid); n != 0 {
		t.Fatalf("portal_request_drafts rows survived offboarding: %d", n)
	}
}

func TestOffboardUser_CollectsRemoteRevocations(t *testing.T) {
	db, uid := newOffboardEnv(t)
	uidText := strconv.Itoa(uid)

	// OAuth SCM connection with client credentials: revocable.
	offboardExec(t, db, `
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled, oauth_client_id, oauth_client_secret_encrypted)
		VALUES ('github-main', 'GitHub', 'github', 'oauth', true, 'gh-client-id', 'enc-gh-secret')
	`)
	ghProviderID := offboardCount(t, db, `SELECT id FROM scm_providers WHERE slug = 'github-main'`)
	offboardExec(t, db, `
		INSERT INTO user_scm_oauth_tokens (user_id, scm_provider_id, oauth_access_token_encrypted)
		VALUES (?, ?, 'enc-gh-token')
	`, uid, ghProviderID)

	// PAT SCM connection: local row must be deleted but not collected for
	// remote revocation.
	offboardExec(t, db, `
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled, personal_access_token_encrypted)
		VALUES ('gitea-pat', 'Gitea', 'gitea', 'pat', true, 'enc-pat')
	`)
	patProviderID := offboardCount(t, db, `SELECT id FROM scm_providers WHERE slug = 'gitea-pat'`)
	offboardExec(t, db, `
		INSERT INTO user_scm_oauth_tokens (user_id, scm_provider_id, oauth_access_token_encrypted)
		VALUES (?, ?, 'enc-gitea-token')
	`, uid, patProviderID)

	// Todoist integration token: revocable.
	offboardExec(t, db, `
		INSERT INTO integration_providers (id, slug, name, provider_type, oauth_client_id, oauth_client_secret_encrypted)
		VALUES ('prov-todoist', 'todoist-main', 'Todoist', 'todoist', 'td-client-id', 'enc-td-secret')
	`)
	offboardExec(t, db, `
		INSERT INTO user_integration_tokens (id, user_id, integration_provider_id, oauth_access_token_encrypted)
		VALUES ('uit-1', ?, 'prov-todoist', 'enc-td-token')
	`, uidText)

	result, err := OffboardUser(db, uid, nil)
	if err != nil {
		t.Fatalf("OffboardUser: %v", err)
	}

	if n := offboardCount(t, db, `SELECT COUNT(*) FROM user_scm_oauth_tokens WHERE user_id = ?`, uid); n != 0 {
		t.Fatalf("user_scm_oauth_tokens rows survived offboarding: %d", n)
	}
	if n := offboardCount(t, db, `SELECT COUNT(*) FROM user_integration_tokens`); n != 0 {
		t.Fatalf("user_integration_tokens rows survived offboarding: %d", n)
	}

	if len(result.RemoteRevocations) != 2 {
		t.Fatalf("expected 2 remote revocations (github oauth + todoist), got %d: %+v",
			len(result.RemoteRevocations), result.RemoteRevocations)
	}
	byKind := map[string]PendingRemoteRevocation{}
	for _, rev := range result.RemoteRevocations {
		byKind[rev.Kind+"-"+rev.ProviderType] = rev
	}
	scmRev, ok := byKind[RevocationKindSCM+"-github"]
	if !ok {
		t.Fatalf("missing github SCM revocation: %+v", result.RemoteRevocations)
	}
	if scmRev.EncryptedAccessToken != "enc-gh-token" || scmRev.OAuthClientID != "gh-client-id" || scmRev.EncryptedClientSecret != "enc-gh-secret" {
		t.Fatalf("github revocation material mismatch: %+v", scmRev)
	}
	intRev, ok := byKind[RevocationKindIntegration+"-todoist"]
	if !ok {
		t.Fatalf("missing todoist integration revocation: %+v", result.RemoteRevocations)
	}
	if intRev.EncryptedAccessToken != "enc-td-token" || intRev.OAuthClientID != "td-client-id" {
		t.Fatalf("todoist revocation material mismatch: %+v", intRev)
	}
	if _, exists := byKind[RevocationKindSCM+"-gitea"]; exists {
		t.Fatal("PAT-based SCM connection must not be collected for remote revocation")
	}
}
