//go:build test

package services

import (
	"context"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// itemSupportEventsEnv holds a workspace with a customer-facing ticket and an
// internal item, plus open/done statuses wired into the workspace workflow.
type itemSupportEventsEnv struct {
	db           database.Database
	comments     *CommentService
	workflow     *WorkflowService
	authorID     int
	ticketID     int
	internalID   int
	openStatus   int
	doneStatus   int
	actorUserID  int
	portalCustID int
	workspaceID  int
	channelID    int
	itemTypeID   int
}

func newItemSupportEventsEnv(t *testing.T) *itemSupportEventsEnv {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	db := tdb.DB
	insertID := func(query string, args ...any) int {
		t.Helper()
		return testutils.InsertID(t, db, query, args...)
	}

	workspaceID := insertID(`INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Support facts', 'SUP', true, false)`)
	authorID := insertID(`INSERT INTO users (email, username, first_name, last_name) VALUES ('support-agent@example.com', 'support-agent', 'S', 'A')`)
	actorUserID := authorID
	portalCustID := insertID(`INSERT INTO portal_customers (name, email) VALUES ('Customer', 'cust@example.com')`)

	var openCatID, doneCatID int64
	if err := db.QueryRow(`INSERT INTO status_categories (name, color, is_completed) VALUES ('Facts Open', '#3b82f6', false) RETURNING id`).Scan(&openCatID); err != nil {
		t.Fatalf("insert open category: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO status_categories (name, color, is_completed) VALUES ('Facts Done', '#22c55e', true) RETURNING id`).Scan(&doneCatID); err != nil {
		t.Fatalf("insert done category: %v", err)
	}
	openStatus := insertID(`INSERT INTO statuses (name, category_id) VALUES ('Facts Open S', ?)`, openCatID)
	doneStatus := insertID(`INSERT INTO statuses (name, category_id) VALUES ('Facts Done S', ?)`, doneCatID)

	workflowID := insertID(`INSERT INTO workflows (name, description, is_default) VALUES ('support-facts', '', false)`)
	insertID(`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, workflowID, openStatus, doneStatus)
	configurationSetID := insertID(`INSERT INTO configuration_sets (name, workflow_id) VALUES ('support-facts', ?)`, workflowID)
	insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, configurationSetID)
	var defaultItemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types WHERE is_default = true LIMIT 1`).Scan(&defaultItemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	// Bind the full catalog so hierarchy-aware operations (split) can pick a
	// legal child type under the default Initiative.
	if _, err := db.Exec(`
		INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id)
		SELECT ?, id FROM item_types
	`, configurationSetID); err != nil {
		t.Fatalf("bind item types to config set: %v", err)
	}

	channelID := insertID(`INSERT INTO channels (name, type, direction, status) VALUES ('Email intake', 'imap', 'inbound', 'enabled')`)

	createItem := func(title string, channel *int) int {
		t.Helper()
		itemID64, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: workspaceID,
			ItemTypeID:  &defaultItemTypeID,
			Title:       title,
			StatusID:    &openStatus,
			CreatorID:   &actorUserID,
			ChannelID:   channel,
		})
		if err != nil {
			t.Fatalf("create item %s: %v", title, err)
		}
		return int(itemID64)
	}
	ticketID := createItem("Customer ticket", &channelID)
	internalID := createItem("Internal task", nil)

	return &itemSupportEventsEnv{
		db:           db,
		comments:     NewCommentService(db),
		workflow:     NewWorkflowService(db),
		authorID:     authorID,
		ticketID:     ticketID,
		internalID:   internalID,
		openStatus:   openStatus,
		doneStatus:   doneStatus,
		actorUserID:  actorUserID,
		portalCustID: portalCustID,
		workspaceID:  workspaceID,
		channelID:    channelID,
		itemTypeID:   defaultItemTypeID,
	}
}

func (e *itemSupportEventsEnv) eventCounts(t *testing.T, itemID int) map[string]int {
	t.Helper()
	counts, err := repository.NewItemSupportEventRepository(e.db).CountByKind(itemID)
	if err != nil {
		t.Fatalf("count support events for item %d: %v", itemID, err)
	}
	return counts
}

func (e *itemSupportEventsEnv) commitTransition(t *testing.T, itemID, fromStatus, toStatus int) {
	t.Helper()
	itemRepo := repository.NewItemRepository(e.db)
	tx, err := e.db.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := e.workflow.CommitTransition(context.Background(), tx, itemRepo, itemID, fromStatus, toStatus, e.actorUserID, itemevents.Metadata{}); err != nil {
		t.Fatalf("commit transition %d -> %d: %v", fromStatus, toStatus, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
}

func TestCommentFirstResponseRecordedOnceForTickets(t *testing.T) {
	env := newItemSupportEventsEnv(t)
	at := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)

	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, AuthorID: env.authorID, Content: "first reply", ActorUserID: env.authorID, CreatedAt: &at,
	}); err != nil {
		t.Fatalf("create public comment: %v", err)
	}
	counts := env.eventCounts(t, env.ticketID)
	if counts[models.SupportEventFirstResponse] != 1 {
		t.Fatalf("first_response count = %d, want 1", counts[models.SupportEventFirstResponse])
	}

	later := at.Add(time.Hour)
	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, AuthorID: env.authorID, Content: "second reply", ActorUserID: env.authorID, CreatedAt: &later,
	}); err != nil {
		t.Fatalf("create second comment: %v", err)
	}
	if counts := env.eventCounts(t, env.ticketID); counts[models.SupportEventFirstResponse] != 1 {
		t.Fatalf("first_response count after second comment = %d, want still 1", counts[models.SupportEventFirstResponse])
	}

	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, AuthorID: env.authorID, Content: "internal note", IsPrivate: true, ActorUserID: env.authorID,
	}); err != nil {
		t.Fatalf("create private comment: %v", err)
	}
	if counts := env.eventCounts(t, env.ticketID); counts[models.SupportEventFirstResponse] != 1 {
		t.Fatalf("private comment changed first_response count to %d, want 1", counts[models.SupportEventFirstResponse])
	}

	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, PortalCustomerID: &env.portalCustID, Content: "customer reply",
	}); err != nil {
		t.Fatalf("create portal comment: %v", err)
	}
	if counts := env.eventCounts(t, env.ticketID); counts[models.SupportEventFirstResponse] != 1 {
		t.Fatalf("portal comment changed first_response count to %d, want 1", counts[models.SupportEventFirstResponse])
	}

	var occurredAt time.Time
	if err := env.db.QueryRow(`SELECT occurred_at FROM item_support_events WHERE item_id = ? AND kind = 'first_response'`, env.ticketID).Scan(&occurredAt); err != nil {
		t.Fatalf("load first_response event: %v", err)
	}
	if !occurredAt.Equal(at) {
		t.Fatalf("first_response occurred_at = %v, want %v", occurredAt, at)
	}
}

func TestCommentFirstResponseSkipsInternalItems(t *testing.T) {
	env := newItemSupportEventsEnv(t)

	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.internalID, AuthorID: env.authorID, Content: "public internal reply", ActorUserID: env.authorID,
	}); err != nil {
		t.Fatalf("create internal comment: %v", err)
	}
	counts := env.eventCounts(t, env.internalID)
	if len(counts) != 0 {
		t.Fatalf("internal item produced events %v, want none", counts)
	}
}

func TestTransitionFactsResolveAndReopen(t *testing.T) {
	env := newItemSupportEventsEnv(t)

	env.commitTransition(t, env.ticketID, env.openStatus, env.doneStatus)
	counts := env.eventCounts(t, env.ticketID)
	if counts[models.SupportEventResolved] != 1 {
		t.Fatalf("resolved count = %d, want 1", counts[models.SupportEventResolved])
	}
	if counts[models.SupportEventReopened] != 0 {
		t.Fatalf("reopened count after close = %d, want 0", counts[models.SupportEventReopened])
	}

	env.commitTransition(t, env.ticketID, env.doneStatus, env.doneStatus)
	if counts := env.eventCounts(t, env.ticketID); counts[models.SupportEventResolved] != 1 {
		t.Fatalf("done-to-done changed resolved count to %d, want 1", counts[models.SupportEventResolved])
	}

	env.commitTransition(t, env.ticketID, env.doneStatus, env.openStatus)
	counts = env.eventCounts(t, env.ticketID)
	if counts[models.SupportEventReopened] != 1 {
		t.Fatalf("reopened count = %d, want 1", counts[models.SupportEventReopened])
	}
	if counts[models.SupportEventResolved] != 1 {
		t.Fatalf("reopen changed resolved count to %d, want 1 (first close)", counts[models.SupportEventResolved])
	}

	env.commitTransition(t, env.ticketID, env.openStatus, env.doneStatus)
	counts = env.eventCounts(t, env.ticketID)
	if counts[models.SupportEventResolved] != 1 {
		t.Fatalf("re-resolve changed resolved count to %d, want 1 (first close)", counts[models.SupportEventResolved])
	}
	if counts[models.SupportEventReopened] != 1 {
		t.Fatalf("re-resolve changed reopened count to %d, want 1", counts[models.SupportEventReopened])
	}
}

func TestTransitionFactsSkipInternalItems(t *testing.T) {
	env := newItemSupportEventsEnv(t)

	env.commitTransition(t, env.internalID, env.openStatus, env.doneStatus)
	env.commitTransition(t, env.internalID, env.doneStatus, env.openStatus)
	counts := env.eventCounts(t, env.internalID)
	if len(counts) != 0 {
		t.Fatalf("internal item produced transition events %v, want none", counts)
	}
}

// TestItemSupportEventsMigrationBackfill replays the 20261011 migration over
// seeded comments and history rows and asserts exact fact extraction and
// idempotence on repeated Initialize.
func TestItemSupportEventsMigrationBackfill(t *testing.T) {
	env := newItemSupportEventsEnv(t)

	replyOne := "2026-01-05 10:30:00"
	replyTwo := "2026-01-05 11:00:00"
	closedAt := "2026-01-06 09:00:00"
	reopenedAt := "2026-01-07 09:00:00"

	insertComment := func(itemID int, isPrivate bool, createdAt string) {
		t.Helper()
		testutils.InsertID(t, env.db, `
			INSERT INTO comments (item_id, author_id, content, is_private, created_at, updated_at)
			VALUES (?, ?, 'text', ?, ?, ?)`, itemID, env.authorID, isPrivate, createdAt, createdAt)
	}
	insertComment(env.ticketID, false, replyTwo)
	insertComment(env.ticketID, false, replyOne)
	insertComment(env.ticketID, true, replyOne)
	insertComment(env.internalID, false, replyOne)

	insertHistory := func(itemID int, oldStatus, newStatus int, changedAt string) {
		t.Helper()
		testutils.InsertID(t, env.db, `
			INSERT INTO item_history (item_id, user_id, field_name, old_value, new_value, changed_at)
			VALUES (?, ?, 'status_id', ?, ?, ?)`,
			itemID, env.actorUserID, oldStatus, newStatus, changedAt)
	}
	insertHistory(env.ticketID, env.openStatus, env.doneStatus, closedAt)
	insertHistory(env.ticketID, env.doneStatus, env.openStatus, reopenedAt)
	insertHistory(env.internalID, env.openStatus, env.doneStatus, closedAt)

	for _, query := range []string{
		"DROP TABLE item_support_events",
		"DELETE FROM schema_migrations WHERE version = '20261011_item_support_events'",
	} {
		if _, err := env.db.ExecWrite(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.db.Initialize(); err != nil {
		t.Fatalf("re-run migrations: %v", err)
	}

	assertFacts := func(stage string) {
		t.Helper()
		var firstResponse, resolved, reopened *time.Time
		if err := env.db.QueryRow(`SELECT occurred_at FROM item_support_events WHERE item_id = ? AND kind = 'first_response'`, env.ticketID).Scan(&firstResponse); err != nil {
			t.Fatalf("%s: first_response missing: %v", stage, err)
		}
		if err := env.db.QueryRow(`SELECT occurred_at FROM item_support_events WHERE item_id = ? AND kind = 'resolved'`, env.ticketID).Scan(&resolved); err != nil {
			t.Fatalf("%s: resolved missing: %v", stage, err)
		}
		rows, err := env.db.Query(`SELECT occurred_at FROM item_support_events WHERE item_id = ? AND kind = 'reopened' ORDER BY occurred_at`, env.ticketID)
		if err != nil {
			t.Fatalf("%s: query reopened: %v", stage, err)
		}
		defer rows.Close()
		var reopenTimes []time.Time
		for rows.Next() {
			var at time.Time
			if err := rows.Scan(&at); err != nil {
				t.Fatalf("%s: scan reopened: %v", stage, err)
			}
			reopenTimes = append(reopenTimes, at)
		}
		if len(reopenTimes) != 1 {
			t.Fatalf("%s: reopened count = %d, want 1", stage, len(reopenTimes))
		}
		reopened = &reopenTimes[0]

		wantReply := mustParseTime(t, replyOne)
		if !firstResponse.Equal(wantReply) {
			t.Fatalf("%s: first_response = %v, want earliest public comment %v", stage, firstResponse, wantReply)
		}
		wantClosed := mustParseTime(t, closedAt)
		if !resolved.Equal(wantClosed) {
			t.Fatalf("%s: resolved = %v, want first completed entry %v", stage, resolved, wantClosed)
		}
		wantReopened := mustParseTime(t, reopenedAt)
		if !reopened.Equal(wantReopened) {
			t.Fatalf("%s: reopened = %v, want %v", stage, reopened, wantReopened)
		}
	}
	assertFacts("after backfill")

	if err := env.db.Initialize(); err != nil {
		t.Fatalf("second initialize: %v", err)
	}
	assertFacts("after idempotent re-run")

	counts := env.eventCounts(t, env.internalID)
	if len(counts) != 0 {
		t.Fatalf("internal item produced backfilled events %v, want none", counts)
	}
	var stamps int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = '20261011_item_support_events'`).Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("migration stamps = %d, want 1", stamps)
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02 15:04:05", value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}
