package services

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/allegro/bigcache/v3"

	"windshift/internal/cacheutil"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

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

		result, err := db.ExecWrite(`
			INSERT INTO users (email, username, first_name, last_name)
			VALUES ('active@example.test', 'active-user', 'Active', 'User')
		`)
		if err != nil {
			t.Fatalf("insert user: %v", err)
		}
		userID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
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

	userResult, err := baseDB.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('generation@example.test', 'generation-user', 'Generation', 'User', true)
	`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, err := userResult.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	assigneeResult, err := baseDB.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('assignee@example.test', 'assignee-user', 'Assigned', 'User', true)
	`)
	if err != nil {
		t.Fatalf("insert assigned user: %v", err)
	}
	assigneeID, err := assigneeResult.LastInsertId()
	if err != nil {
		t.Fatalf("assigned user LastInsertId: %v", err)
	}

	workspaceResult, err := baseDB.ExecWrite(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Generation Workspace', 'GEN', '', true, false)
	`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace LastInsertId: %v", err)
	}

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

func TestAccessibleWorkspaceIDsUsesOneDecodeAndNoWarmDatabaseQueries(t *testing.T) {
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
		result, err := baseDB.ExecWrite(`
			INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
			VALUES (?, ?, '', ?, false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, name, key, active)
		if err != nil {
			t.Fatalf("insert workspace %s: %v", key, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
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
	before := service.GetWorkspaceAccessStats()
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
	afterCold := service.GetWorkspaceAccessStats()
	if afterCold.PermissionSnapshotDecodes-before.PermissionSnapshotDecodes != 1 {
		t.Fatalf("cold permission decodes delta = %d, want 1", afterCold.PermissionSnapshotDecodes-before.PermissionSnapshotDecodes)
	}

	countingDB.calls.Store(0)
	ids, err = service.AccessibleWorkspaceIDs(userID)
	if err != nil {
		t.Fatalf("warm AccessibleWorkspaceIDs: %v", err)
	}
	if calls := countingDB.calls.Load(); calls != 0 {
		t.Fatalf("warm accessible-workspace DB calls = %d, want 0", calls)
	}
	afterWarm := service.GetWorkspaceAccessStats()
	if afterWarm.PermissionSnapshotDecodes-afterCold.PermissionSnapshotDecodes != 1 {
		t.Fatalf("warm permission decodes delta = %d, want 1", afterWarm.PermissionSnapshotDecodes-afterCold.PermissionSnapshotDecodes)
	}
	if afterWarm.ActiveWorkspaceCacheHits == 0 {
		t.Fatal("warm active-workspace cache hit was not recorded")
	}

	newID := insertWorkspace("New", "NEW", true)
	snapshot, err := service.getUserPermissionCache(userID)
	if err != nil {
		t.Fatalf("get permission cache: %v", err)
	}
	snapshot.WorkspaceEveryone[newID] = map[string]bool{models.PermissionItemView: true}
	if err := service.storeUserPermissionCache(userID, snapshot); err != nil {
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
	cacheConfig := cacheutil.NewBigCacheConfig(cacheutil.BigCacheOptions{
		TTL:             time.Hour,
		MaxCacheMB:      32,
		Shards:          16,
		MaxEntrySize:    1024 * 1024,
		MaxEntriesInWin: 100,
		CleanWindow:     time.Minute,
	})
	cache, err := bigcache.New(context.Background(), cacheConfig)
	if err != nil {
		tb.Fatalf("bigcache.New: %v", err)
	}
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
	for _, workspaceCount := range []int{10, 100, 1000} {
		t.Run(fmt.Sprintf("%d_workspaces", workspaceCount), func(t *testing.T) {
			service := newSyntheticWorkspaceAccessService(t, workspaceCount)
			before := service.GetWorkspaceAccessStats().PermissionSnapshotDecodes
			ids, err := service.AccessibleWorkspaceIDs(1)
			if err != nil {
				t.Fatalf("AccessibleWorkspaceIDs: %v", err)
			}
			if len(ids) != workspaceCount {
				t.Fatalf("accessible workspace count = %d, want %d", len(ids), workspaceCount)
			}
			decodes := service.GetWorkspaceAccessStats().PermissionSnapshotDecodes - before
			if decodes != 1 {
				t.Fatalf("permission snapshot decodes = %d, want 1", decodes)
			}
		})
	}
}

func BenchmarkAccessibleWorkspaceIDs(b *testing.B) {
	for _, workspaceCount := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("legacy_%d_workspaces_parallel", workspaceCount), func(b *testing.B) {
			service := newSyntheticWorkspaceAccessService(b, workspaceCount)
			before := service.GetWorkspaceAccessStats().PermissionSnapshotDecodes
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
			decodes := service.GetWorkspaceAccessStats().PermissionSnapshotDecodes - before
			b.ReportMetric(float64(decodes)/float64(b.N), "permission-decodes/op")
		})

		b.Run(fmt.Sprintf("snapshot_%d_workspaces_parallel", workspaceCount), func(b *testing.B) {
			service := newSyntheticWorkspaceAccessService(b, workspaceCount)
			before := service.GetWorkspaceAccessStats().PermissionSnapshotDecodes
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
			decodes := service.GetWorkspaceAccessStats().PermissionSnapshotDecodes - before
			b.ReportMetric(float64(decodes)/float64(b.N), "permission-decodes/op")
		})
	}
}
