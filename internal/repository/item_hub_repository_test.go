//go:build test

package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/testutils"
)

type hubInboxFixture struct {
	UserID     int
	Workspace  int
	ItemID     int
	CustomerID int
}

// seedHubInboxItem creates the minimal production rows a portal submission
// needs: a workspace, status, portal channel with the given config, portal
// customer, and an item linking them. This is a repository test, so the rows
// are inserted directly against the production schema.
func seedHubInboxItem(t *testing.T, tdb *testutils.TestDB, channelConfig, fracIndex string) hubInboxFixture {
	t.Helper()
	now := time.Now()

	var userID int
	if err := tdb.QueryRow(`
		INSERT INTO users (username, email, first_name, last_name, password_hash, created_at, updated_at)
		VALUES ('hub-user', 'hub-user@example.com', 'Hub', 'User', 'hash', ?, ?) RETURNING id
	`, now, now).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, created_at, updated_at)
		VALUES ('Hub Workspace', 'HUB', 'hub test', ?, ?) RETURNING id
	`, now, now).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	var statusID int
	if err := tdb.QueryRow(`SELECT id FROM statuses LIMIT 1`).Scan(&statusID); err != nil {
		t.Fatalf("select status: %v", err)
	}

	channelID := testutils.InsertID(t, tdb, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Support Portal', 'portal', 'inbound', 'enabled', ?)
	`, channelConfig)

	var customerID int
	if err := tdb.QueryRow(`
		INSERT INTO portal_customers (name, email, created_at, updated_at)
		VALUES ('Portal Customer', 'portal-customer@example.com', ?, ?) RETURNING id
	`, now, now).Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}

	var itemID int
	if err := tdb.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, status_id, is_task,
		                   frac_index, path, channel_id, creator_id, creator_portal_customer_id, created_at, updated_at)
		VALUES (?, 1, 'Portal request', 'Please help', ?, false, ?, '/1/', ?, ?, ?, ?, ?)
		RETURNING id
	`, workspaceID, statusID, fracIndex, channelID, userID, customerID, now, now).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	return hubInboxFixture{UserID: userID, Workspace: workspaceID, ItemID: itemID, CustomerID: customerID}
}

func TestListAndFindHubInboxItemReturnPortalSlug(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	fixture := seedHubInboxItem(t, tdb, `{"portal_slug":"support"}`, "hub0")
	repo := NewItemRepository(tdb.GetDatabase())
	ctx := context.Background()

	items, total, facets, err := repo.ListHubInboxItems(ctx, HubInboxFilter{UserID: fixture.UserID, PerPage: 10})
	if err != nil {
		t.Fatalf("ListHubInboxItems: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("inbox total/len = %d/%d, want 1/1", total, len(items))
	}
	if items[0].PortalSlug != "support" {
		t.Fatalf("portal slug = %q, want %q", items[0].PortalSlug, "support")
	}
	if items[0].PortalName != "Support Portal" {
		t.Fatalf("portal name = %q, want %q", items[0].PortalName, "Support Portal")
	}
	if len(facets) != 1 {
		t.Fatalf("facet count = %d, want 1", len(facets))
	}

	found, err := repo.FindHubInboxItem(ctx, fixture.UserID, fixture.ItemID)
	if err != nil {
		t.Fatalf("FindHubInboxItem: %v", err)
	}
	if found.PortalSlug != "support" {
		t.Fatalf("single-item portal slug = %q, want %q", found.PortalSlug, "support")
	}
	if found.SubmitterName == nil || *found.SubmitterName != "Portal Customer" {
		t.Fatalf("submitter name = %v, want Portal Customer", found.SubmitterName)
	}
}

func TestListHubInboxItemsToleratesMalformedChannelConfig(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	fixture := seedHubInboxItem(t, tdb, `not-json`, "hub1")
	repo := NewItemRepository(tdb.GetDatabase())

	items, total, _, err := repo.ListHubInboxItems(context.Background(), HubInboxFilter{UserID: fixture.UserID, PerPage: 10})
	if err != nil {
		t.Fatalf("ListHubInboxItems: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("inbox total/len = %d/%d, want 1/1", total, len(items))
	}
	if items[0].PortalSlug != "" {
		t.Fatalf("portal slug = %q, want empty for malformed config", items[0].PortalSlug)
	}

	found, err := repo.FindHubInboxItem(context.Background(), fixture.UserID, fixture.ItemID)
	if err != nil {
		t.Fatalf("FindHubInboxItem: %v", err)
	}
	if found.PortalSlug != "" {
		t.Fatalf("single-item portal slug = %q, want empty for malformed config", found.PortalSlug)
	}
}
