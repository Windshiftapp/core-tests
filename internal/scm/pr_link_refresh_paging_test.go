//go:build test

package scm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// A link set larger than one page must still be fully refreshed, and the link
// scan must be keyset-paged with a LIMIT instead of materializing the whole
// item_scm_links table on every pass.
func TestRefreshAllPRLinkStatesPagesLargeLinkSets(t *testing.T) {
	const linkCount = 450

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	itemID, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "SCM paging item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	repoID := createSCMTriggerRepository(t, db, data.WorkspaceID, nil)
	var connectionID int
	if err := db.QueryRow("SELECT workspace_scm_connection_id FROM workspace_repositories WHERE id = ?", repoID).Scan(&connectionID); err != nil {
		t.Fatalf("load connection: %v", err)
	}

	const batchSize = 50
	for start := 1; start <= linkCount; start += batchSize {
		end := start + batchSize - 1
		var values strings.Builder
		args := make([]any, 0, (end-start+1)*4)
		for number := start; number <= end; number++ {
			if number > start {
				values.WriteString(", ")
			}
			values.WriteString("(?, ?, 'pull_request', ?, ?, 'open')")
			args = append(args, itemID, repoID, fmt.Sprintf("%d", number), fmt.Sprintf("https://example.test/owner/repo/pull/%d", number))
		}
		if _, err := db.Exec(
			`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id, external_url, state) VALUES `+values.String(),
			args...,
		); err != nil {
			t.Fatalf("seed PR links %d-%d: %v", start, end, err)
		}
	}

	scans := &linkScanQueryCounter{Database: db}
	provider := &fakeProvider{pullRequest: &PullRequest{
		Number: 1, Title: "PR refreshed", State: "open",
		URL: "https://example.test/owner/repo/pull/1", Author: User{ID: "7", Name: "SCM User"},
	}}
	service := NewSyncService(scans, nil)
	service.resolveProviderOverride = func(_ context.Context, gotConnectionID int) (Provider, error) {
		if gotConnectionID != connectionID {
			t.Fatalf("resolved connection = %d, want %d", gotConnectionID, connectionID)
		}
		return provider, nil
	}

	if err := service.RefreshAllPRLinkStates(t.Context()); err != nil {
		t.Fatalf("RefreshAllPRLinkStates(): %v", err)
	}

	var refreshed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_scm_links WHERE title = 'PR refreshed'`).Scan(&refreshed); err != nil {
		t.Fatalf("count refreshed links: %v", err)
	}
	if refreshed != linkCount {
		t.Fatalf("refreshed %d of %d links; paging skipped or duplicated rows", refreshed, linkCount)
	}

	scans.mu.Lock()
	defer scans.mu.Unlock()
	if scans.total < 2 {
		t.Fatalf("link refresh issued %d link-scan queries; a large set must be read in multiple pages", scans.total)
	}
	if scans.unbounded > 0 {
		t.Fatalf("%d of %d link-scan queries had no keyset/LIMIT bound; the whole table would be materialized", scans.unbounded, scans.total)
	}

	healthRepo := repository.NewSCMHealthRepository(db)
	connections, err := healthRepo.ListConnectionDiagnostics(t.Context())
	if err != nil {
		t.Fatalf("list connection health: %v", err)
	}
	diagnostic := findConnectionHealth(t, connections, connectionID, repository.SCMHealthOperationPRLinkRefresh)
	if diagnostic.State != repository.SCMHealthStateHealthy || diagnostic.CheckedResources != linkCount || diagnostic.FailedResources != 0 {
		t.Fatalf("health after paging run = %+v, want healthy with %d checked resources", diagnostic, linkCount)
	}
}

// linkScanQueryCounter counts queries that read refreshable pull_request
// links and flags any that do so without a keyset predicate and LIMIT.
type linkScanQueryCounter struct {
	database.Database
	mu        sync.Mutex
	total     int
	unbounded int
}

func (db *linkScanQueryCounter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "item_scm_links") && strings.Contains(query, "link_type = 'pull_request'") {
		db.mu.Lock()
		db.total++
		if !strings.Contains(query, "LIMIT") || !strings.Contains(query, "isl.id > ?") {
			db.unbounded++
		}
		db.mu.Unlock()
	}
	return db.Database.QueryContext(ctx, query, args...)
}
