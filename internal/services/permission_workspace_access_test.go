package services

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/cacheutil"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type failingPermissionQueryDatabase struct {
	database.Database
	fragment string
}

type blockingItemLookupDatabase struct {
	database.Database
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (db *blockingItemLookupDatabase) QueryRow(query string, args ...interface{}) *sql.Row {
	if strings.Contains(query, "SELECT workspace_id FROM items WHERE id") {
		db.once.Do(func() { close(db.started) })
		<-db.release
	}
	return db.Database.QueryRow(query, args...)
}

func (db *failingPermissionQueryDatabase) Query(query string, args ...interface{}) (*sql.Rows, error) {
	if strings.Contains(query, db.fragment) {
		return nil, errors.New("injected permission query failure")
	}
	return db.Database.Query(query, args...)
}

func TestPermissionSnapshotFailsClosedWhenExplicitAssignmentsCannotLoad(t *testing.T) {
	baseDB, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "permission-fail-closed.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = baseDB.Close() })
	if err := baseDB.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	userID := testutils.InsertID(t, baseDB, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('fail-closed@example.test', 'fail-closed', 'Fail', 'Closed', true)
	`)

	wrapped := &failingPermissionQueryDatabase{
		Database: baseDB,
		fragment: "SELECT DISTINCT workspace_id, role_id FROM user_workspace_roles",
	}
	service, err := NewPermissionService(wrapped, PermissionCacheConfig{
		TTL: time.Minute, MaxCacheSize: 16, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	if _, err := service.GetUserEffectivePermissions(int(userID)); err == nil {
		t.Fatal("permission snapshot succeeded with incomplete explicit assignments")
	}
	if _, err := service.getUserPermissionCache(int(userID)); err == nil {
		t.Fatal("incomplete permission snapshot was cached")
	}
}

func TestBatchWorkspacePermissionsAreIndependentOfCacheWarmth(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "permission-warmth.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	userID := testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('warmth@example.test', 'warmth', 'Cache', 'Warmth', true)
	`)
	workspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Warmth', 'WARM', '', true, false)
	`)
	if _, err := db.ExecWrite(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		SELECT ?, ?, id FROM workspace_roles WHERE builtin_key = ?
	`, userID, workspaceID, models.RoleBuiltinTester); err != nil {
		t.Fatalf("assign Tester role: %v", err)
	}

	service, err := NewPermissionService(db, PermissionCacheConfig{
		TTL: time.Minute, MaxCacheSize: 16, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	permissions := []string{models.PermissionItemView, models.PermissionItemEdit, models.PermissionTestExecute}

	cold, err := service.HasWorkspacePermissions(int(userID), int(workspaceID), permissions)
	if err != nil {
		t.Fatalf("cold HasWorkspacePermissions: %v", err)
	}
	warm, err := service.HasWorkspacePermissions(int(userID), int(workspaceID), permissions)
	if err != nil {
		t.Fatalf("warm HasWorkspacePermissions: %v", err)
	}
	for _, permission := range permissions {
		if cold[permission] != warm[permission] {
			t.Fatalf("permission %q changed with cache warmth: cold=%v warm=%v", permission, cold, warm)
		}
	}
	if !cold[models.PermissionItemEdit] {
		t.Fatalf("implicit Editor permission missing from union: %v", cold)
	}
}

func TestBuiltInRoleRenameDoesNotChangeEveryoneAuthorization(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "permission-role-rename.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	userID := testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('role-rename@example.test', 'role-rename', 'Role', 'Rename', true)
	`)
	workspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Role Rename', 'RENAME', '', true, false)
	`)
	service, err := NewPermissionService(db, PermissionCacheConfig{TTL: time.Minute, MaxCacheSize: 16, BatchSize: 10})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	before, err := service.HasWorkspacePermission(int(userID), int(workspaceID), models.PermissionItemView)
	if err != nil {
		t.Fatalf("permission before rename: %v", err)
	}
	if !before {
		t.Fatal("open Viewer role did not grant item.view before rename")
	}
	if _, err := db.ExecWrite(`UPDATE workspace_roles SET name = 'Renamed Viewer' WHERE builtin_key = ?`, models.RoleBuiltinViewer); err != nil {
		t.Fatalf("rename Viewer: %v", err)
	}
	if err := service.InvalidateUserCache(int(userID)); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}
	after, err := service.HasWorkspacePermission(int(userID), int(workspaceID), models.PermissionItemView)
	if err != nil {
		t.Fatalf("permission after rename: %v", err)
	}
	if after != before {
		t.Fatalf("item.view changed after built-in role rename: before=%v after=%v", before, after)
	}
}

func TestItemWorkspaceLookupCannotRestoreInvalidatedPermissionSnapshot(t *testing.T) {
	baseDB, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "item-lookup-generation.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = baseDB.Close() })
	if err := baseDB.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	workspaceID := testutils.InsertID(t, baseDB, `INSERT INTO workspaces (name, key, active) VALUES ('Item Lookup', 'LOOKUP', true)`)
	itemID, err := CreateItem(baseDB, ItemCreationParams{
		WorkspaceID: int(workspaceID),
		Title:       "Lookup Item",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	wrapped := &blockingItemLookupDatabase{
		Database: baseDB,
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	service, err := NewPermissionService(wrapped, PermissionCacheConfig{TTL: time.Minute, MaxCacheSize: 16, BatchSize: 10})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	const userID = 99
	now := time.Now()
	if err := service.storeUserPermissionCache(userID, &models.UserPermissionCache{
		UserID: userID,
		WorkspacePermissions: map[int]map[string]bool{
			int(workspaceID): {models.PermissionWorkspaceAdmin: true},
		},
		WorkspaceEveryone: map[int]map[string]bool{},
		CachedAt:          now,
		ExpiresAt:         now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("store stale snapshot: %v", err)
	}

	lookupDone := make(chan error, 1)
	go func() {
		_, lookupErr := service.GetItemWorkspaceID(userID, int(itemID))
		lookupDone <- lookupErr
	}()
	<-wrapped.started
	if err := service.InvalidateUserCache(userID); err != nil {
		t.Fatalf("InvalidateUserCache: %v", err)
	}
	close(wrapped.release)
	if err := <-lookupDone; err != nil {
		t.Fatalf("GetItemWorkspaceID: %v", err)
	}
	if _, err := service.getUserPermissionCache(userID); err == nil {
		t.Fatal("item lookup restored the invalidated permission snapshot")
	}
}

func TestWorkspacePermissionFromSnapshot(t *testing.T) {
	snapshot := &models.UserPermissionCache{
		WorkspaceEveryone: map[int]map[string]bool{
			1: {models.PermissionItemView: true},
		},
		WorkspacePermissions: map[int]map[string]bool{
			2: {models.PermissionItemView: true},
		},
	}

	if !workspacePermissionFromSnapshot(snapshot, 1, models.PermissionItemView) {
		t.Fatal("Everyone item.view permission was not honored")
	}
	if !workspacePermissionFromSnapshot(snapshot, 2, models.PermissionItemView) {
		t.Fatal("direct/role item.view permission was not honored")
	}
	if workspacePermissionFromSnapshot(snapshot, 3, models.PermissionItemView) {
		t.Fatal("gated workspace without a grant was accessible")
	}
	snapshot.IsSystemAdmin = true
	if !workspacePermissionFromSnapshot(snapshot, 999, models.PermissionItemView) {
		t.Fatal("system administrator did not receive workspace permission")
	}
}

func TestRecentlyActiveUsersUsesCurrentSessionAndUserSchema(t *testing.T) {
	t.Run("active sessions", func(t *testing.T) {
		db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "active-sessions.db"))
		if err != nil {
			t.Fatalf("NewSQLiteDB: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := db.Initialize(); err != nil {
			t.Fatalf("Initialize: %v", err)
		}

		userID := testutils.InsertID(t, db, `
			INSERT INTO users (email, username, first_name, last_name)
			VALUES ('active@example.test', 'active-user', 'Active', 'User')
		`)
		if _, err := db.ExecWrite(`
			INSERT INTO user_sessions (user_id, session_token, expires_at, is_active)
			VALUES (?, 'active-session', ?, true)
		`, userID, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("insert session: %v", err)
		}

		service := &PermissionService{db: db, batchSize: 10}
		users, err := service.getRecentlyActiveUsers(24 * time.Hour)
		if err != nil {
			t.Fatalf("getRecentlyActiveUsers: %v", err)
		}
		if len(users) != 1 || users[0] != int(userID) {
			t.Fatalf("recent users = %v, want [%d]", users, userID)
		}
	})

	t.Run("fallback active users", func(t *testing.T) {
		db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "fallback-users.db"))
		if err != nil {
			t.Fatalf("NewSQLiteDB: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		for _, statement := range []string{
			`CREATE TABLE users (id INTEGER PRIMARY KEY, is_active BOOLEAN, updated_at DATETIME)`,
			`INSERT INTO users (id, is_active, updated_at) VALUES (1, true, CURRENT_TIMESTAMP), (2, false, CURRENT_TIMESTAMP)`,
		} {
			if _, err := db.ExecWrite(statement); err != nil {
				t.Fatalf("exec %q: %v", statement, err)
			}
		}

		service := &PermissionService{db: db, batchSize: 10}
		users, err := service.getRecentlyActiveUsers(24 * time.Hour)
		if err != nil {
			t.Fatalf("getRecentlyActiveUsers fallback: %v", err)
		}
		if len(users) != 1 || users[0] != 1 {
			t.Fatalf("fallback users = %v, want [1]", users)
		}
	})
}

type countingPermissionDatabase struct {
	database.Database
	calls atomic.Int64
}

type invalidatingPermissionDatabase struct {
	database.Database
	once        sync.Once
	service     *PermissionService
	calls       atomic.Int64
	workspaceID int
	assigneeID  int
	mutationErr error
}

func (db *invalidatingPermissionDatabase) Query(query string, args ...interface{}) (*sql.Rows, error) {
	if db.service != nil && db.calls.Add(1) == 3 {
		db.once.Do(func() {
			_, db.mutationErr = db.Database.ExecWrite(`
				INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
				SELECT ?, ?, id FROM workspace_roles WHERE name = ?
			`, db.assigneeID, db.workspaceID, models.RoleViewer)
			if db.mutationErr == nil {
				db.service.OnEveryoneAccessChanged()
			}
		})
	}
	return db.Database.Query(query, args...)
}

func TestEffectivePermissionSnapshotRetriesWhenInvalidatedDuringBuild(t *testing.T) {
	baseDB, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "permission-generation.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = baseDB.Close() })
	if err := baseDB.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	userID := testutils.InsertID(t, baseDB, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('generation@example.test', 'generation-user', 'Generation', 'User', true)
	`)

	assigneeID := testutils.InsertID(t, baseDB, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('assignee@example.test', 'assignee-user', 'Assigned', 'User', true)
	`)

	workspaceID := testutils.InsertID(t, baseDB, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Generation Workspace', 'GEN', '', true, false)
	`)

	wrappedDB := &invalidatingPermissionDatabase{
		Database:    baseDB,
		workspaceID: int(workspaceID),
		assigneeID:  int(assigneeID),
	}
	service, err := NewPermissionService(wrappedDB, PermissionCacheConfig{
		TTL:          time.Minute,
		MaxCacheSize: 16,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = service.cache.Close() })
	wrappedDB.service = service

	snapshot, err := service.GetUserEffectivePermissions(int(userID))
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions: %v", err)
	}
	if snapshot.UserID != int(userID) {
		t.Fatalf("snapshot user = %d, want %d", snapshot.UserID, userID)
	}
	if wrappedDB.mutationErr != nil {
		t.Fatalf("restrict viewer role during permission build: %v", wrappedDB.mutationErr)
	}
	if workspacePermissionFromSnapshot(snapshot, int(workspaceID), models.PermissionItemView) {
		t.Fatal("snapshot retained stale Everyone access after viewer role became restricted")
	}
	cached, err := service.getUserPermissionCache(int(userID))
	if err != nil {
		t.Fatalf("fresh permission snapshot was not cached: %v", err)
	}
	if workspacePermissionFromSnapshot(cached, int(workspaceID), models.PermissionItemView) {
		t.Fatal("stale Everyone access was committed to the permission cache")
	}
}

func (db *countingPermissionDatabase) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.calls.Add(1)
	return db.Database.Query(query, args...)
}

func (db *countingPermissionDatabase) QueryRow(query string, args ...interface{}) *sql.Row {
	db.calls.Add(1)
	return db.Database.QueryRow(query, args...)
}

func TestAccessibleWorkspaceIDsReusesWarmSnapshotWithoutDecode(t *testing.T) {
	baseDB, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "permissions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = baseDB.Close() })
	if err := baseDB.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertWorkspace := func(name, key string, active bool) int {
		t.Helper()
		id := testutils.InsertID(t, baseDB, `
			INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
			VALUES (?, ?, '', ?, false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, name, key, active)
		return int(id)
	}

	openID := insertWorkspace("Open", "OPEN", true)
	gatedID := insertWorkspace("Gated", "GATED", true)
	inactiveID := insertWorkspace("Inactive", "INACTIVE", false)

	countingDB := &countingPermissionDatabase{Database: baseDB}
	service, err := NewPermissionService(countingDB, PermissionCacheConfig{
		TTL:          time.Minute,
		MaxCacheSize: 16,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = service.cache.Close() })

	const userID = 42
	now := time.Now()
	if err := service.storeUserPermissionCache(userID, &models.UserPermissionCache{
		UserID: userID,
		WorkspaceEveryone: map[int]map[string]bool{
			openID:     {models.PermissionItemView: true},
			inactiveID: {models.PermissionItemView: true},
		},
		WorkspacePermissions: map[int]map[string]bool{},
		CachedAt:             now,
		ExpiresAt:            now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("store permission cache: %v", err)
	}

	countingDB.calls.Store(0)
	before := service.GetCacheStats()
	ids, err := service.AccessibleWorkspaceIDs(userID)
	if err != nil {
		t.Fatalf("first AccessibleWorkspaceIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != openID {
		t.Fatalf("accessible IDs = %v, want [%d]; gated=%d inactive=%d", ids, openID, gatedID, inactiveID)
	}
	if calls := countingDB.calls.Load(); calls != 1 {
		t.Fatalf("cold active-workspace DB calls = %d, want 1", calls)
	}
	afterCold := service.GetCacheStats()
	if afterCold.PermissionSnapshotDecodes != before.PermissionSnapshotDecodes {
		t.Fatalf("cold permission decodes changed from %d to %d, want none", before.PermissionSnapshotDecodes, afterCold.PermissionSnapshotDecodes)
	}
	if hits := afterCold.Hits - before.Hits; hits != 1 {
		t.Fatalf("cold permission cache hits = %d, want 1", hits)
	}

	countingDB.calls.Store(0)
	ids, err = service.AccessibleWorkspaceIDs(userID)
	if err != nil {
		t.Fatalf("warm AccessibleWorkspaceIDs: %v", err)
	}
	if calls := countingDB.calls.Load(); calls != 0 {
		t.Fatalf("warm accessible-workspace DB calls = %d, want 0", calls)
	}
	afterWarm := service.GetCacheStats()
	if afterWarm.PermissionSnapshotDecodes != afterCold.PermissionSnapshotDecodes {
		t.Fatalf("warm permission decodes changed from %d to %d, want none", afterCold.PermissionSnapshotDecodes, afterWarm.PermissionSnapshotDecodes)
	}
	if hits := afterWarm.Hits - afterCold.Hits; hits != 1 {
		t.Fatalf("warm permission cache hits = %d, want 1", hits)
	}
	if afterWarm.ActiveWorkspaceCacheHits == 0 {
		t.Fatal("warm active-workspace cache hit was not recorded")
	}

	newID := insertWorkspace("New", "NEW", true)
	// Store a fresh snapshot: cached snapshots are shared pointers and must not
	// be mutated in place.
	if err := service.storeUserPermissionCache(userID, &models.UserPermissionCache{
		UserID: userID,
		WorkspaceEveryone: map[int]map[string]bool{
			openID:     {models.PermissionItemView: true},
			inactiveID: {models.PermissionItemView: true},
			newID:      {models.PermissionItemView: true},
		},
		WorkspacePermissions: map[int]map[string]bool{},
		CachedAt:             time.Now(),
		ExpiresAt:            time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("update permission cache: %v", err)
	}
	service.InvalidateActiveWorkspaceCache()
	countingDB.calls.Store(0)
	ids, err = service.AccessibleWorkspaceIDs(userID)
	if err != nil {
		t.Fatalf("AccessibleWorkspaceIDs after invalidation: %v", err)
	}
	if len(ids) != 2 || ids[0] != openID || ids[1] != newID {
		t.Fatalf("accessible IDs after invalidation = %v, want [%d %d]", ids, openID, newID)
	}
	if calls := countingDB.calls.Load(); calls != 1 {
		t.Fatalf("post-invalidation DB calls = %d, want 1", calls)
	}
}

func newSyntheticWorkspaceAccessService(tb testing.TB, workspaceCount int) *PermissionService {
	tb.Helper()
	cache := cacheutil.NewTyped[*models.UserPermissionCache]("permission_workspace_access_test", cacheutil.TypedCacheOptions{
		TTL:    time.Hour,
		Shards: 16,
	})
	tb.Cleanup(func() { _ = cache.Close() })

	service := &PermissionService{
		cache:           cache,
		ttl:             time.Hour,
		workspaceAccess: newWorkspaceAccessCache(),
	}
	pairs := make([]repository.IDKey, workspaceCount)
	everyone := make(map[int]map[string]bool, workspaceCount)
	for i := range workspaceCount {
		id := i + 1
		pairs[i] = repository.IDKey{ID: id, Key: fmt.Sprintf("WS%d", id)}
		everyone[id] = map[string]bool{models.PermissionItemView: true}
	}
	service.workspaceAccess.active.Store(&activeWorkspaceSet{
		epoch:     service.workspaceAccess.epoch.Load(),
		expiresAt: time.Now().Add(time.Hour),
		pairs:     pairs,
	})
	now := time.Now()
	if err := service.storeUserPermissionCache(1, &models.UserPermissionCache{
		UserID:               1,
		WorkspaceEveryone:    everyone,
		WorkspacePermissions: map[int]map[string]bool{},
		CachedAt:             now,
		ExpiresAt:            now.Add(time.Hour),
	}); err != nil {
		tb.Fatalf("store permission cache: %v", err)
	}
	return service
}

func TestAccessibleWorkspaceIDsDecodeCountDoesNotScale(t *testing.T) {
	baseDB, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "permission-query-growth.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = baseDB.Close() })
	countingDB := &countingPermissionDatabase{Database: baseDB}

	for _, workspaceCount := range []int{10, 1000} {
		t.Run(fmt.Sprintf("%d_workspaces", workspaceCount), func(t *testing.T) {
			service := newSyntheticWorkspaceAccessService(t, workspaceCount)
			service.db = countingDB
			countingDB.calls.Store(0)
			before := service.GetCacheStats()
			ids, err := service.AccessibleWorkspaceIDs(1)
			if err != nil {
				t.Fatalf("AccessibleWorkspaceIDs: %v", err)
			}
			if len(ids) != workspaceCount {
				t.Fatalf("accessible workspace count = %d, want %d", len(ids), workspaceCount)
			}
			after := service.GetCacheStats()
			if decodes := after.PermissionSnapshotDecodes - before.PermissionSnapshotDecodes; decodes != 0 {
				t.Fatalf("permission snapshot decodes = %d, want 0", decodes)
			}
			if hits := after.Hits - before.Hits; hits != 1 {
				t.Fatalf("permission cache hits = %d, want 1", hits)
			}
			if calls := countingDB.calls.Load(); calls != 0 {
				t.Fatalf("warm permission-store queries = %d, want 0", calls)
			}
		})
	}
}

func BenchmarkAccessibleWorkspaceIDs(b *testing.B) {
	for _, workspaceCount := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("legacy_%d_workspaces_parallel", workspaceCount), func(b *testing.B) {
			service := newSyntheticWorkspaceAccessService(b, workspaceCount)
			before := service.GetCacheStats().Hits
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					for workspaceID := 1; workspaceID <= workspaceCount; workspaceID++ {
						hasView, err := service.HasWorkspacePermission(1, workspaceID, models.PermissionItemView)
						if err != nil || !hasView {
							b.Fatalf("HasWorkspacePermission(%d): allowed=%t err=%v", workspaceID, hasView, err)
						}
					}
				}
			})
			b.StopTimer()
			hits := service.GetCacheStats().Hits - before
			b.ReportMetric(float64(hits)/float64(b.N), "permission-cache-hits/op")
		})

		b.Run(fmt.Sprintf("snapshot_%d_workspaces_parallel", workspaceCount), func(b *testing.B) {
			service := newSyntheticWorkspaceAccessService(b, workspaceCount)
			before := service.GetCacheStats().Hits
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					ids, err := service.AccessibleWorkspaceIDs(1)
					if err != nil || len(ids) != workspaceCount {
						b.Fatalf("AccessibleWorkspaceIDs: len=%d err=%v", len(ids), err)
					}
				}
			})
			b.StopTimer()
			hits := service.GetCacheStats().Hits - before
			b.ReportMetric(float64(hits)/float64(b.N), "permission-cache-hits/op")
		})
	}
}
