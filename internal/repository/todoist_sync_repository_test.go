package repository

import (
	"errors"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

// seedTodoistProvider inserts a minimal enabled provider row so the sync config
// FK (integration_provider_id -> integration_providers.id) is satisfied.
func seedTodoistProvider(t *testing.T, db database.Database, id string) {
	t.Helper()
	_, err := db.ExecWrite(`INSERT INTO integration_providers (id, slug, name, provider_type, enabled)
		VALUES (?, ?, ?, 'todoist', TRUE)`, id, "todoist-"+id, "Todoist")
	if err != nil {
		t.Fatalf("seed provider: %v", err)
	}
}

func newTodoistRepoDB(t *testing.T) database.Database {
	t.Helper()
	// A per-test NAMED in-memory DB: the anonymous `file::memory:?cache=shared`
	// DSN is one process-wide database shared with every other test file using
	// it, so our plain CREATE TABLE collides with tables created there.
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// NewSQLiteDB does not apply the integrations schema for the in-memory DSN,
	// so create exactly the tables this repo touches (mirrors schema/integrations.sql).
	for _, ddl := range []string{
		`CREATE TABLE integration_providers (
			id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, name TEXT NOT NULL,
			provider_type TEXT NOT NULL, enabled BOOLEAN NOT NULL DEFAULT TRUE
		)`,
		`CREATE TABLE todoist_sync_config (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, integration_provider_id TEXT NOT NULL,
			personal_workspace_id INTEGER NOT NULL, enabled BOOLEAN DEFAULT FALSE,
			scope_mode TEXT NOT NULL DEFAULT 'all', todoist_project_id TEXT DEFAULT '',
			sync_token TEXT DEFAULT '*', last_synced_at DATETIME, last_error TEXT DEFAULT '',
			sync_lock_until DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, integration_provider_id)
		)`,
		`CREATE TABLE todoist_task_links (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, item_id INTEGER NOT NULL,
			todoist_task_id TEXT NOT NULL, todoist_project_id TEXT DEFAULT '',
			last_title TEXT DEFAULT '', last_description TEXT DEFAULT '', last_due TEXT DEFAULT '',
			last_priority INTEGER DEFAULT 1, last_completed BOOLEAN DEFAULT FALSE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, todoist_task_id), UNIQUE(item_id)
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db
}

func TestTodoistSyncConfigUpsertAndGet(t *testing.T) {
	db := newTodoistRepoDB(t)
	seedTodoistProvider(t, db, "prov-1")
	repo := NewTodoistSyncRepository(db)

	if _, err := repo.GetConfig("7", "prov-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetConfig before insert = %v, want ErrNotFound", err)
	}

	cfg := models.TodoistSyncConfig{
		ID:                    "cfg-1",
		UserID:                "7",
		IntegrationProviderID: "prov-1",
		PersonalWorkspaceID:   42,
		Enabled:               true,
		ScopeMode:             models.TodoistScopeProject,
		TodoistProjectID:      "p-99",
	}
	if err := repo.UpsertConfig(cfg); err != nil {
		t.Fatalf("UpsertConfig: %v", err)
	}

	got, err := repo.GetConfig("7", "prov-1")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.PersonalWorkspaceID != 42 || !got.Enabled || got.ScopeMode != models.TodoistScopeProject || got.TodoistProjectID != "p-99" {
		t.Errorf("config mismatch: %+v", got)
	}
	if got.SyncToken != "*" {
		t.Errorf("fresh config SyncToken = %q, want *", got.SyncToken)
	}
	if got.LastSyncedAt != nil {
		t.Errorf("fresh config LastSyncedAt = %v, want nil", got.LastSyncedAt)
	}

	// Upsert again with new scope: token must reset to '*' for a full re-sync.
	cfg.ScopeMode = models.TodoistScopeAll
	cfg.TodoistProjectID = ""
	cfg.Enabled = false
	if err := repo.UpsertConfig(cfg); err != nil {
		t.Fatalf("UpsertConfig (update): %v", err)
	}
	got, _ = repo.GetConfig("7", "prov-1")
	if got.ScopeMode != models.TodoistScopeAll || got.Enabled {
		t.Errorf("updated config not applied: %+v", got)
	}
}

func TestTodoistSyncStateAndEnabledListing(t *testing.T) {
	db := newTodoistRepoDB(t)
	seedTodoistProvider(t, db, "prov-1")
	repo := NewTodoistSyncRepository(db)

	_ = repo.UpsertConfig(models.TodoistSyncConfig{ID: "cfg-on", UserID: "1", IntegrationProviderID: "prov-1", PersonalWorkspaceID: 1, Enabled: true, ScopeMode: models.TodoistScopeAll})
	_ = repo.UpsertConfig(models.TodoistSyncConfig{ID: "cfg-off", UserID: "2", IntegrationProviderID: "prov-1", PersonalWorkspaceID: 2, Enabled: false, ScopeMode: models.TodoistScopeAll})

	enabled, err := repo.ListEnabledConfigs()
	if err != nil {
		t.Fatalf("ListEnabledConfigs: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != "cfg-on" {
		t.Fatalf("enabled configs = %+v, want only cfg-on", enabled)
	}

	if err := repo.UpdateSyncState("cfg-on", "token-xyz", ""); err != nil {
		t.Fatalf("UpdateSyncState: %v", err)
	}
	got, _ := repo.GetConfig("1", "prov-1")
	if got.SyncToken != "token-xyz" {
		t.Errorf("SyncToken = %q, want token-xyz", got.SyncToken)
	}
	if got.LastSyncedAt == nil {
		t.Error("LastSyncedAt should be set after UpdateSyncState")
	}
}

// TestTodoistSyncLock covers the per-config admission lock the sync service
// uses to keep a manual "Sync now" and the 5-minute poller from reconciling the
// same config at once: one acquirer wins, a concurrent acquirer is rejected
// while the lease is live, release frees it, and a stale (expired) lease is
// reclaimable without an explicit release (a crashed holder self-heals).
func TestTodoistSyncLock(t *testing.T) {
	db := newTodoistRepoDB(t)
	seedTodoistProvider(t, db, "prov-1")
	repo := NewTodoistSyncRepository(db)
	if err := repo.UpsertConfig(models.TodoistSyncConfig{
		ID: "cfg-1", UserID: "1", IntegrationProviderID: "prov-1",
		PersonalWorkspaceID: 1, Enabled: true, ScopeMode: models.TodoistScopeAll,
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	now := time.Now().UTC()
	until := now.Add(10 * time.Minute)

	if ok, err := repo.AcquireSyncLock("cfg-1", now, until); err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v, want true/nil", ok, err)
	}
	// Concurrent acquire while the lease is live is rejected.
	if ok, err := repo.AcquireSyncLock("cfg-1", now, until); err != nil || ok {
		t.Fatalf("second acquire: ok=%v err=%v, want false/nil", ok, err)
	}
	// Release frees it.
	if err := repo.ReleaseSyncLock("cfg-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, err := repo.AcquireSyncLock("cfg-1", now, until); err != nil || !ok {
		t.Fatalf("re-acquire after release: ok=%v err=%v, want true/nil", ok, err)
	}
	// A stale lease (now advanced past the lease end) is reclaimable.
	stale := until.Add(time.Hour)
	if ok, err := repo.AcquireSyncLock("cfg-1", stale, stale.Add(10*time.Minute)); err != nil || !ok {
		t.Fatalf("acquire after stale lease: ok=%v err=%v, want true/nil", ok, err)
	}
}

func TestTodoistTaskLinkLifecycle(t *testing.T) {
	db := newTodoistRepoDB(t)
	repo := NewTodoistSyncRepository(db)

	if _, err := repo.GetLinkByItemID("1", 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLinkByItemID before insert = %v, want ErrNotFound", err)
	}

	link := models.TodoistTaskLink{
		ID: "link-1", UserID: "1", ItemID: 100, TodoistTaskID: "td-5", TodoistProjectID: "p-1",
		LastTitle: "Buy milk", LastDescription: "2%", LastDue: "2026-07-01", LastPriority: 2, LastCompleted: false,
	}
	if err := repo.UpsertLink(link); err != nil {
		t.Fatalf("UpsertLink: %v", err)
	}

	byItem, err := repo.GetLinkByItemID("1", 100)
	if err != nil {
		t.Fatalf("GetLinkByItemID: %v", err)
	}
	byTodoist, err := repo.GetLinkByTodoistID("1", "td-5")
	if err != nil {
		t.Fatalf("GetLinkByTodoistID: %v", err)
	}
	if byItem.ID != "link-1" || byTodoist.ID != "link-1" {
		t.Errorf("link lookups disagree: %+v / %+v", byItem, byTodoist)
	}
	if byItem.LastTitle != "Buy milk" || byItem.LastPriority != 2 || byItem.LastDue != "2026-07-01" {
		t.Errorf("snapshot not persisted: %+v", byItem)
	}

	// Update snapshot via upsert on the item_id key.
	link.LastTitle = "Buy oat milk"
	link.LastCompleted = true
	if err := repo.UpsertLink(link); err != nil {
		t.Fatalf("UpsertLink (update): %v", err)
	}
	byItem, _ = repo.GetLinkByItemID("1", 100)
	if byItem.LastTitle != "Buy oat milk" || !byItem.LastCompleted {
		t.Errorf("snapshot update not applied: %+v", byItem)
	}

	links, err := repo.ListLinksByUser("1")
	if err != nil || len(links) != 1 {
		t.Fatalf("ListLinksByUser = %v (err %v), want 1", links, err)
	}

	if err := repo.DeleteLink("link-1"); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	if _, err := repo.GetLinkByItemID("1", 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete = %v, want ErrNotFound", err)
	}
}
