//go:build test

package repository

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestZammadObservabilityFollowsCredentialWorkspaceScope(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID := testutils.InsertID(t, db, `INSERT INTO users (email, username, first_name, last_name) VALUES ('zammad-observation@example.test', 'zammad-observation', 'Zammad', 'Observation')`)
	workspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key) VALUES ('Observed workspace', 'ZOBS')`)
	itemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, frac_index, creator_id) VALUES (?, 1, 'Observed item', 'a', ?)`, workspaceID, userID)
	credentialID := testutils.InsertID(t, db, `INSERT INTO action_credentials (name, credential_type, applies_to_all_workspaces, encrypted_secret, is_enabled) VALUES ('Zammad observation credential', 'custom_header', false, 'synthetic', true)`)
	if _, err := db.ExecWrite(`INSERT INTO integration_providers (id, slug, name, provider_type, enabled) VALUES ('zammad-observation', 'zammad-observation', 'Zammad observation', 'zammad', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`INSERT INTO zammad_connections (provider_id, credential_id, base_url, default_customer) VALUES ('zammad-observation', ?, 'https://zammad.example.test', 'robot@example.test')`, credentialID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`INSERT INTO item_integration_links (id, item_id, integration_provider_id, external_id, external_url, title, link_type, linked_by) VALUES ('zammad-observation-external', ?, 'zammad-observation', '901', 'https://zammad.example.test/#ticket/zoom/901', 'Observed ticket', 'ticket', ?)`, itemID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`INSERT INTO zammad_ticket_links (id, item_id, provider_id, item_integration_link_id, ticket_id, ticket_number, correlation_key, sync_state, created_by) VALUES ('zammad-observation-link', ?, 'zammad-observation', 'zammad-observation-external', 901, '901', 'ZOBS-1', ?, ?)`, itemID, models.ZammadSyncFailed, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`INSERT INTO zammad_ticket_changes (id, ticket_link_id, field_name, new_value_id, new_value_name) VALUES ('zammad-observation-change', 'zammad-observation-link', 'status', 2, 'open')`); err != nil {
		t.Fatal(err)
	}

	repo := NewZammadRepository(db)
	assertVisible := func(want bool) {
		t.Helper()
		changes, err := repo.ListTicketChangesForItem(itemID, 10)
		if err != nil || (len(changes) == 1) != want {
			t.Fatalf("item changes: visible=%v rows=%d err=%v", want, len(changes), err)
		}
		recent, err := repo.ListRecentTicketChangesForWorkspace(workspaceID, 10)
		if err != nil || (len(recent) == 1) != want {
			t.Fatalf("recent changes: visible=%v rows=%d err=%v", want, len(recent), err)
		}
		tickets, err := repo.ListOverviewTicketsForWorkspace(workspaceID, 10)
		if err != nil || (len(tickets) == 1) != want {
			t.Fatalf("overview tickets: visible=%v rows=%d err=%v", want, len(tickets), err)
		}
		links, err := repo.ListTicketLinksForWorkspace(workspaceID)
		if err != nil || (len(links) == 1) != want {
			t.Fatalf("overview links: visible=%v rows=%d err=%v", want, len(links), err)
		}
		failed, uncertain, err := repo.CountProblemTicketLinksForWorkspace(workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		wantedFailed := 0
		if want {
			wantedFailed = 1
		}
		if failed != wantedFailed || uncertain != 0 {
			t.Fatalf("problem counts: failed=%d uncertain=%d want failed=%d", failed, uncertain, wantedFailed)
		}
	}
	assertVisible(false)
	if _, err := db.ExecWrite(`INSERT INTO action_credential_workspaces (credential_id, workspace_id) VALUES (?, ?)`, credentialID, workspaceID); err != nil {
		t.Fatal(err)
	}
	assertVisible(true)
}
