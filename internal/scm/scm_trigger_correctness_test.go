//go:build test

package scm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type durableSCMRecorder struct {
	mu     sync.Mutex
	events []*models.ActionEvent
	err    error
}

func (r *durableSCMRecorder) EmitActionEventInTx(_ context.Context, _ database.Tx, event *models.ActionEvent) error {
	if r.err != nil {
		return r.err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *durableSCMRecorder) eventTypes() []models.ActionTriggerType {
	r.mu.Lock()
	defer r.mu.Unlock()
	types := make([]models.ActionTriggerType, 0, len(r.events))
	for _, event := range r.events {
		types = append(types, event.EventType)
	}
	slices.Sort(types)
	return types
}

func TestFirstRepositorySyncBaselinesExistingRefs(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	repoID := createSCMTriggerRepository(t, db, data.WorkspaceID, nil)
	recorder := &durableSCMRecorder{}
	service := NewSyncService(db, nil)
	service.SetDurableActionEvents(recorder)
	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	provider := &fakeProvider{
		branches: []Branch{{Name: "release/1.0", SHA: "branch-old"}},
		tags:     []Tag{{Name: "v1.0", SHA: "tag-old", CreatedAt: now.Add(-24 * time.Hour)}},
	}

	if err := service.syncRepository(t.Context(), provider, repoID, "owner/repo", "main", data.WorkspaceID, "TST", "", time.Time{}); err != nil {
		t.Fatalf("first syncRepository() error = %v", err)
	}
	if got := recorder.eventTypes(); len(got) != 0 {
		t.Fatalf("first-sync events = %v, want baseline without replay", got)
	}
	assertProcessedRefCount(t, db, repoID, 2)

	var lastSyncedAt time.Time
	if err := db.QueryRow("SELECT last_synced_at FROM workspace_repositories WHERE id = ?", repoID).Scan(&lastSyncedAt); err != nil {
		t.Fatalf("load first-sync checkpoint: %v", err)
	}
	provider.branches = append(provider.branches, Branch{Name: "release/2.0", SHA: "branch-new"})
	provider.tags = append(provider.tags, Tag{Name: "v2.0", SHA: "tag-new", CreatedAt: now})
	if err := service.syncRepository(t.Context(), provider, repoID, "owner/repo", "main", data.WorkspaceID, "TST", "", lastSyncedAt); err != nil {
		t.Fatalf("second syncRepository() error = %v", err)
	}
	want := []models.ActionTriggerType{
		models.ActionTriggerSCMReleaseBranchCreated,
		models.ActionTriggerSCMTagCreated,
	}
	slices.Sort(want)
	if got := recorder.eventTypes(); !slices.Equal(got, want) {
		t.Fatalf("steady-state events = %v, want %v", got, want)
	}
	assertProcessedRefCount(t, db, repoID, 4)
}

func TestFailedDurableRefAdmissionDoesNotAdvanceSyncCheckpoint(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	checkpoint := time.Date(2026, time.August, 27, 19, 0, 0, 0, time.UTC)
	repoID := createSCMTriggerRepository(t, db, data.WorkspaceID, &checkpoint)
	service := NewSyncService(db, nil)
	service.SetDurableActionEvents(&durableSCMRecorder{err: errors.New("durable admission failed")})
	provider := &fakeProvider{
		tags: []Tag{{Name: "v2.0", SHA: "tag-new", CreatedAt: checkpoint.Add(time.Minute)}},
	}

	err := service.syncRepository(t.Context(), provider, repoID, "owner/repo", "main", data.WorkspaceID, "TST", "", checkpoint)
	if err == nil || !strings.Contains(err.Error(), "durable admission failed") {
		t.Fatalf("syncRepository() error = %v, want durable admission failure", err)
	}
	var stored time.Time
	if err := db.QueryRow("SELECT last_synced_at FROM workspace_repositories WHERE id = ?", repoID).Scan(&stored); err != nil {
		t.Fatalf("load retained checkpoint: %v", err)
	}
	if !stored.Equal(checkpoint) {
		t.Fatalf("last_synced_at = %s, want unchanged %s", stored, checkpoint)
	}
	assertProcessedRefCount(t, db, repoID, 0)
}

func TestFailedDurablePRAdmissionRollsBackLinkAndCheckpoint(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	itemID, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "SCM trigger item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	var itemNumber int
	if err := db.QueryRow("SELECT workspace_item_number FROM items WHERE id = ?", itemID).Scan(&itemNumber); err != nil {
		t.Fatalf("load item number: %v", err)
	}
	checkpoint := time.Date(2026, time.August, 27, 19, 0, 0, 0, time.UTC)
	repoID := createSCMTriggerRepository(t, db, data.WorkspaceID, &checkpoint)
	service := NewSyncService(db, nil)
	service.SetDurableActionEvents(&durableSCMRecorder{err: errors.New("durable PR admission failed")})
	provider := &fakeProvider{pages: [][]PullRequest{{
		{
			Number: 7, Title: fmt.Sprintf("TEST-%d durable event", itemNumber),
			State: "open", URL: "https://example.test/pr/7",
			Author: User{ID: "scm-user", Name: "SCM User"}, UpdatedAt: checkpoint.Add(time.Minute),
		},
	}}}

	err = service.syncRepository(t.Context(), provider, repoID, "owner/repo", "main", data.WorkspaceID, "TEST", "", checkpoint)
	if err == nil || !strings.Contains(err.Error(), "durable PR admission failed") {
		t.Fatalf("syncRepository() error = %v, want durable PR admission failure", err)
	}
	var links int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM item_scm_links
		WHERE item_id = ? AND workspace_repository_id = ?
	`, itemID, repoID).Scan(&links); err != nil {
		t.Fatalf("count rolled-back PR links: %v", err)
	}
	if links != 0 {
		t.Fatalf("PR links = %d, want transaction rollback", links)
	}
	var stored time.Time
	if err := db.QueryRow("SELECT last_synced_at FROM workspace_repositories WHERE id = ?", repoID).Scan(&stored); err != nil {
		t.Fatalf("load retained checkpoint: %v", err)
	}
	if !stored.Equal(checkpoint) {
		t.Fatalf("last_synced_at = %s, want unchanged %s", stored, checkpoint)
	}
}

func createSCMTriggerRepository(t *testing.T, db database.Database, workspaceID int, lastSyncedAt *time.Time) int {
	t.Helper()
	var providerID int
	if err := db.QueryRow(`
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled)
		VALUES ('wi-708-provider', 'WI-708 Provider', 'github', 'pat', true)
		RETURNING id
	`).Scan(&providerID); err != nil {
		t.Fatalf("create SCM provider: %v", err)
	}
	var connectionID int
	if err := db.QueryRow(`
		INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
		VALUES (?, ?, true)
		RETURNING id
	`, workspaceID, providerID).Scan(&connectionID); err != nil {
		t.Fatalf("create SCM connection: %v", err)
	}
	var repoID int
	if err := db.QueryRow(`
		INSERT INTO workspace_repositories (
			workspace_scm_connection_id, repository_external_id, repository_name,
			repository_url, default_branch, last_synced_at
		) VALUES (?, 'repo-1', 'owner/repo', 'https://example.test/owner/repo', 'main', ?)
		RETURNING id
	`, connectionID, lastSyncedAt).Scan(&repoID); err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}
	return repoID
}

func assertProcessedRefCount(t *testing.T, db database.Database, repoID, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM scm_processed_refs WHERE workspace_repository_id = ?", repoID).Scan(&got); err != nil {
		t.Fatalf("count processed refs: %v", err)
	}
	if got != want {
		t.Fatalf("processed refs = %d, want %d", got, want)
	}
}
