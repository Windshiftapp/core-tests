//go:build test

package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

type lifecycleTestEnv struct {
	db         database.Database
	userID     int
	workspaceA int
	workspaceB int
	perm       *PermissionService
	svc        *ItemLifecycleService
}

func setupLifecycleTestEnv(t *testing.T) lifecycleTestEnv {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	// A second workspace for cross-workspace rejection tests. Role grants are
	// unnecessary: authorization flows through the permission cache below.
	if _, err := db.Exec(`
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('Second Workspace', 'TST2', 'Second test workspace', true)
	`); err != nil {
		t.Fatalf("create second workspace: %v", err)
	}
	var workspaceB int
	if err := db.QueryRow(`SELECT id FROM workspaces WHERE key = 'TST2'`).Scan(&workspaceB); err != nil {
		t.Fatalf("load second workspace: %v", err)
	}

	perm, err := NewPermissionService(db, PermissionCacheConfig{TTL: time.Minute, MaxCacheSize: 8, BatchSize: 10})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = perm.cache.Close() })
	now := time.Now()
	if err := perm.storeUserPermissionCache(data.UserID, &models.UserPermissionCache{
		UserID: data.UserID,
		WorkspacePermissions: map[int]map[string]bool{
			data.WorkspaceID: {models.PermissionItemEdit: true, models.PermissionItemView: true},
			workspaceB:       {models.PermissionItemEdit: true, models.PermissionItemView: true},
		},
		CachedAt:  now,
		ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed permission cache: %v", err)
	}

	return lifecycleTestEnv{
		db: db, userID: data.UserID, workspaceA: data.WorkspaceID, workspaceB: workspaceB,
		perm: perm, svc: NewItemLifecycleService(db, perm),
	}
}

// itemTypeIDByName resolves a seeded default item type by name.
func (e lifecycleTestEnv) itemTypeIDByName(t *testing.T, name string) int {
	t.Helper()
	var id int
	if err := e.db.QueryRow(`SELECT id FROM item_types WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("load item type %q: %v", name, err)
	}
	return id
}

// itemTypeIDAtLevel resolves the first catalog type at a hierarchy level.
func (e lifecycleTestEnv) itemTypeIDAtLevel(t *testing.T, level int) int {
	t.Helper()
	var id int
	if err := e.db.QueryRow(`
		SELECT id FROM item_types WHERE hierarchy_level = ? ORDER BY sort_order, id LIMIT 1
	`, level).Scan(&id); err != nil {
		t.Fatalf("load item type at level %d: %v", level, err)
	}
	return id
}

// newItem inserts a minimal Story ticket directly. The lifecycle service under
// test moves rows between existing items; it does not create them. A regular
// mid-hierarchy type keeps split tests meaningful (a Story has a next level).
func (e lifecycleTestEnv) newItem(t *testing.T, workspaceID int, title string) int {
	t.Helper()
	return e.newItemWithType(t, workspaceID, title, e.itemTypeIDAtLevel(t, 2))
}

// newItemWithType inserts a minimal ticket of an explicit item type.
func (e lifecycleTestEnv) newItemWithType(t *testing.T, workspaceID int, title string, itemTypeID int) int {
	t.Helper()
	var itemID int
	if err := e.db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index)
		VALUES (?, (SELECT COALESCE(MAX(workspace_item_number), 0) + 1 FROM items WHERE workspace_id = ?), ?, ?, '', ?)
		RETURNING id
	`, workspaceID, workspaceID, itemTypeID, title, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return itemID
}

func (e lifecycleTestEnv) newComment(t *testing.T, itemID int) int {
	t.Helper()
	var authorID int
	if err := e.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&authorID); err != nil {
		t.Fatalf("load author: %v", err)
	}
	var id int
	if err := e.db.QueryRow(`INSERT INTO comments (item_id, author_id, content) VALUES (?, ?, 'comment') RETURNING id`, itemID, authorID).Scan(&id); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	return id
}

// newItemWithParent inserts a ticket as a child of parentID.
func (e lifecycleTestEnv) newItemWithParent(t *testing.T, workspaceID int, title string, parentID int) int {
	t.Helper()
	var parentPath string
	if err := e.db.QueryRow(`SELECT COALESCE(path, '/') FROM items WHERE id = ?`, parentID).Scan(&parentPath); err != nil {
		t.Fatalf("load parent path: %v", err)
	}
	var itemID int
	if err := e.db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index, parent_id, path)
		VALUES (?, (SELECT COALESCE(MAX(workspace_item_number), 0) + 1 FROM items WHERE workspace_id = ?), ?, ?, '', ?, ?, ?)
		RETURNING id
	`, workspaceID, workspaceID, e.itemTypeIDAtLevel(t, 2), title, testutils.NextTestFracIndex(), parentID, parentPath+fmt.Sprintf("%d/", parentID)).Scan(&itemID); err != nil {
		t.Fatalf("create child item %q: %v", title, err)
	}
	return itemID
}

func (e lifecycleTestEnv) newPortalCustomer(t *testing.T, name, email string) int {
	t.Helper()
	var id int
	if err := e.db.QueryRow(`INSERT INTO portal_customers (name, email) VALUES (?, ?) RETURNING id`, name, email).Scan(&id); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}
	return id
}

// newItemWithRequester inserts a ticket attributed to a portal customer.
func (e lifecycleTestEnv) newItemWithRequester(t *testing.T, workspaceID int, title string, requesterID int) int {
	t.Helper()
	var itemID int
	if err := e.db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, creator_portal_customer_id)
		VALUES (?, (SELECT COALESCE(MAX(workspace_item_number), 0) + 1 FROM items WHERE workspace_id = ?), ?, '', ?, ?)
		RETURNING id
	`, workspaceID, workspaceID, title, testutils.NextTestFracIndex(), requesterID).Scan(&itemID); err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return itemID
}

func (e lifecycleTestEnv) newAttachment(t *testing.T, itemID int) int {
	t.Helper()
	var id int
	if err := e.db.QueryRow(`
		INSERT INTO attachments (item_id, filename, original_filename, file_path, mime_type, file_size, created_at)
		VALUES (?, 'f.bin', 'f.bin', 'items/f.bin', 'application/octet-stream', 1, CURRENT_TIMESTAMP)
		RETURNING id
	`, itemID).Scan(&id); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	return id
}

func (e lifecycleTestEnv) newTrackingRow(t *testing.T, channelID, itemID int, messageID string) {
	t.Helper()
	if channelID == 0 {
		if err := e.db.QueryRow(`INSERT INTO channels (name, type, direction, status) VALUES ('email', 'email', 'inbound', 'enabled') RETURNING id`).Scan(&channelID); err != nil {
			t.Fatalf("insert channel: %v", err)
		}
	}
	if _, err := e.db.Exec(`
		INSERT INTO email_message_tracking (channel_id, message_id, dedup_key, from_email, direction, item_id)
		VALUES (?, ?, ?, 'customer@example.com', 'inbound', ?)
	`, channelID, messageID, messageID, itemID); err != nil {
		t.Fatalf("insert tracking row: %v", err)
	}
}

func (e lifecycleTestEnv) mergedInto(t *testing.T, itemID int) *int {
	t.Helper()
	var mergedInto *int
	if err := e.db.QueryRow(`SELECT merged_into_item_id FROM items WHERE id = ?`, itemID).Scan(&mergedInto); err != nil {
		t.Fatalf("read merged_into_item_id: %v", err)
	}
	return mergedInto
}

func (e lifecycleTestEnv) countDomainEvents(t *testing.T, eventType string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = ?`, eventType).Scan(&n); err != nil {
		t.Fatalf("count %s facts: %v", eventType, err)
	}
	return n
}

// TestItemLifecycle_MergeMovesContentToCanonical verifies the full fold:
// comments, attachments, links, watchers, email threads, and pending replies
// move to the canonical; duplicates keep a durable merged pointer; facts and
// history are recorded.
func TestItemLifecycle_MergeMovesContentToCanonical(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	dupA := env.newItem(t, env.workspaceA, "Dup A")
	dupB := env.newItem(t, env.workspaceA, "Dup B")

	commentA1 := env.newComment(t, dupA)
	commentA2 := env.newComment(t, dupA)
	env.newComment(t, dupB)
	attachment := env.newAttachment(t, dupA)
	env.newTrackingRow(t, 0, dupA, "<thread-a@example.com>")

	var linkTypeID int
	if err := env.db.QueryRow(`
		INSERT INTO link_types (name, forward_label, reverse_label) VALUES ('lifecycle-relates', 'relates to', 'relates to') RETURNING id
	`).Scan(&linkTypeID); err != nil {
		t.Fatalf("insert link type: %v", err)
	}
	otherItem := env.newItem(t, env.workspaceA, "Unrelated")
	if _, err := env.db.Exec(`
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id)
		VALUES (?, 'item', ?, 'item', ?)
	`, linkTypeID, dupA, otherItem); err != nil {
		t.Fatalf("insert item link: %v", err)
	}

	var watcherID int
	if err := env.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&watcherID); err != nil {
		t.Fatalf("load watcher: %v", err)
	}
	if _, err := env.db.Exec(`INSERT INTO item_watches (item_id, user_id) VALUES (?, ?)`, dupA, watcherID); err != nil {
		t.Fatalf("insert watch: %v", err)
	}

	result, err := env.svc.Merge(ctx, ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dupA, dupB},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(result.Sources) != 2 {
		t.Fatalf("sources in result = %d, want 2", len(result.Sources))
	}
	for _, source := range result.Sources {
		if !source.Merged {
			t.Fatalf("source %d reported merged=false, want true", source.SourceItemID)
		}
		if merged := env.mergedInto(t, source.SourceItemID); merged == nil || *merged != canonical {
			t.Fatalf("item %d merged pointer = %v, want %d", source.SourceItemID, merged, canonical)
		}
	}

	var commentsOnCanonical, commentsOnDupA int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, canonical).Scan(&commentsOnCanonical); err != nil {
		t.Fatalf("count canonical comments: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, dupA).Scan(&commentsOnDupA); err != nil {
		t.Fatalf("count dup comments: %v", err)
	}
	if commentsOnCanonical != 3 || commentsOnDupA != 0 {
		t.Fatalf("comments canonical=%d dupA=%d, want 3/0", commentsOnCanonical, commentsOnDupA)
	}

	var attachmentsOnCanonical int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE item_id = ?`, canonical).Scan(&attachmentsOnCanonical); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if attachmentsOnCanonical != 1 {
		t.Fatalf("attachments on canonical = %d, want 1", attachmentsOnCanonical)
	}

	// The link re-pointed to the canonical, not left dangling on the duplicate.
	var linksOnDuplicate, linksOnCanonical int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM item_links WHERE source_id = ? AND source_type = 'item'`, dupA).Scan(&linksOnDuplicate); err != nil {
		t.Fatalf("count dup links: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM item_links WHERE source_id = ? AND source_type = 'item'`, canonical).Scan(&linksOnCanonical); err != nil {
		t.Fatalf("count canonical links: %v", err)
	}
	if linksOnDuplicate != 0 || linksOnCanonical != 1 {
		t.Fatalf("links dup=%d canonical=%d, want 0/1", linksOnDuplicate, linksOnCanonical)
	}

	// Email threading follows the canonical so replies keep appending there.
	var trackingItem *int
	if err := env.db.QueryRow(`SELECT item_id FROM email_message_tracking WHERE message_id = ?`, "<thread-a@example.com>").Scan(&trackingItem); err != nil {
		t.Fatalf("read tracking row: %v", err)
	}
	if trackingItem == nil || *trackingItem != canonical {
		t.Fatalf("tracking item = %v, want %d", trackingItem, canonical)
	}

	var watchesOnDup int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM item_watches WHERE item_id = ?`, dupA).Scan(&watchesOnDup); err != nil {
		t.Fatalf("count dup watches: %v", err)
	}
	if watchesOnDup != 0 {
		t.Fatalf("watches left on duplicate = %d, want 0", watchesOnDup)
	}

	if got := env.countDomainEvents(t, "item.merged"); got != 4 { // 2 facts per merged duplicate
		t.Fatalf("item.merged facts = %d, want 4", got)
	}

	var historyRows int
	if err := env.db.QueryRow(`
		SELECT COUNT(*) FROM item_history WHERE item_id = ? AND field_name = 'merged_into_item_id'
	`, dupA).Scan(&historyRows); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyRows != 1 {
		t.Fatalf("merge history rows = %d, want 1", historyRows)
	}

	// The canonical records the other half of the fold: one entry per
	// duplicate, naming the ticket that was folded in.
	canonicalSources := map[string]bool{}
	rows, err := env.db.Query(`
		SELECT new_value FROM item_history WHERE item_id = ? AND field_name = 'merged_item_id'
	`, canonical)
	if err != nil {
		t.Fatalf("query canonical merge history: %v", err)
	}
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			t.Fatalf("scan canonical merge history: %v", err)
		}
		canonicalSources[sourceID] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate canonical merge history: %v", err)
	}
	_ = rows.Close()
	if len(canonicalSources) != 2 || !canonicalSources[fmt.Sprintf("%d", dupA)] || !canonicalSources[fmt.Sprintf("%d", dupB)] {
		t.Fatalf("canonical merge history = %v, want entries for %d and %d", canonicalSources, dupA, dupB)
	}

	_ = commentA1
	_ = commentA2
	_ = attachment
}

// TestItemLifecycle_MergeKeepsOneIntakeConfirmation verifies the fold cannot
// collide with the per-item intake receipt unique index (WI-1769). The
// canonical's own receipt wins; the duplicate's extra one is dropped, and a
// pending reply still follows the canonical.
func TestItemLifecycle_MergeKeepsOneIntakeConfirmation(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	duplicate := env.newItem(t, env.workspaceA, "Duplicate")

	var channelID int
	if err := env.db.QueryRow(`
		INSERT INTO channels (name, type, direction, status) VALUES ('email', 'email', 'inbound', 'enabled') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	for _, itemID := range []int{canonical, duplicate} {
		if _, err := env.db.Exec(`
			INSERT INTO email_reply_outbox (kind, channel_id, item_id, to_email, subject, html_body, text_body, message_id, from_email)
			VALUES ('intake_confirmation', ?, ?, 'pat@example.test', 'We got it', '<p>hi</p>', 'hi', ?, 'support@example.test')
		`, channelID, itemID, fmt.Sprintf("<confirm-%d@example.test>", itemID)); err != nil {
			t.Fatalf("insert intake confirmation for %d: %v", itemID, err)
		}
	}
	// A pending reply on the duplicate must still follow the canonical.
	if _, err := env.db.Exec(`
		INSERT INTO email_reply_outbox (channel_id, item_id, to_email, subject, html_body, text_body, message_id, from_email)
		VALUES (?, ?, 'pat@example.test', 'Re: hi', '<p>re</p>', 're', '<reply-dup@example.test>', 'support@example.test')
	`, channelID, duplicate); err != nil {
		t.Fatalf("insert pending reply: %v", err)
	}

	if _, err := env.svc.Merge(ctx, ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{duplicate},
	}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var onCanonical, onDuplicate int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE item_id = ? AND kind = 'intake_confirmation'`, canonical).Scan(&onCanonical); err != nil {
		t.Fatalf("count canonical confirmations: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE item_id = ? AND kind = 'intake_confirmation'`, duplicate).Scan(&onDuplicate); err != nil {
		t.Fatalf("count duplicate confirmations: %v", err)
	}
	if onCanonical != 1 || onDuplicate != 0 {
		t.Fatalf("intake confirmations canonical=%d duplicate=%d, want 1/0", onCanonical, onDuplicate)
	}

	var repliesOnCanonical, repliesOnDuplicate int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE item_id = ? AND kind = 'reply'`, canonical).Scan(&repliesOnCanonical); err != nil {
		t.Fatalf("count canonical replies: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE item_id = ? AND kind = 'reply'`, duplicate).Scan(&repliesOnDuplicate); err != nil {
		t.Fatalf("count duplicate replies: %v", err)
	}
	if repliesOnCanonical != 1 || repliesOnDuplicate != 0 {
		t.Fatalf("pending replies canonical=%d duplicate=%d, want 1/0", repliesOnCanonical, repliesOnDuplicate)
	}
}

// TestItemLifecycle_MergeMovesLoneIntakeConfirmation verifies a receipt that
// only the duplicate has still lands on the canonical, so a later reply to
// that receipt threads onto the canonical instead of the read-only redirect.
func TestItemLifecycle_MergeMovesLoneIntakeConfirmation(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	duplicate := env.newItem(t, env.workspaceA, "Duplicate")

	var channelID int
	if err := env.db.QueryRow(`
		INSERT INTO channels (name, type, direction, status) VALUES ('email', 'email', 'inbound', 'enabled') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := env.db.Exec(`
		INSERT INTO email_reply_outbox (kind, channel_id, item_id, to_email, subject, html_body, text_body, message_id, from_email)
		VALUES ('intake_confirmation', ?, ?, 'pat@example.test', 'We got it', '<p>hi</p>', 'hi', '<confirm-dup@example.test>', 'support@example.test')
	`, channelID, duplicate); err != nil {
		t.Fatalf("insert duplicate intake confirmation: %v", err)
	}

	if _, err := env.svc.Merge(ctx, ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{duplicate},
	}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var onCanonical, onDuplicate int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE item_id = ? AND kind = 'intake_confirmation'`, canonical).Scan(&onCanonical); err != nil {
		t.Fatalf("count canonical confirmations: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE item_id = ? AND kind = 'intake_confirmation'`, duplicate).Scan(&onDuplicate); err != nil {
		t.Fatalf("count duplicate confirmations: %v", err)
	}
	if onCanonical != 1 || onDuplicate != 0 {
		t.Fatalf("intake confirmations canonical=%d duplicate=%d, want 1/0", onCanonical, onDuplicate)
	}
}

// recordingLifecycleEmitter captures the post-commit item updates the merge
// flow emits.
type recordingLifecycleEmitter struct {
	updated []int
}

func (e *recordingLifecycleEmitter) EmitItemUpdated(_ *models.Item, updated *models.Item, _ bool, _ bool, _ int, _ []repository.HistoryEntry, _ ...string) {
	e.updated = append(e.updated, updated.ID)
}

func (e *recordingLifecycleEmitter) EmitItemCreated(_ *models.Item, _ int, _ ...string) {}

// TestItemLifecycle_MergeAbandonsRunningSLACycles verifies a merged duplicate
// stops being an SLA-bearing ticket: its running cycles are abandoned so armed
// warning/breach jobs cannot keep firing for content that moved to the
// canonical (WI-1528).
func TestItemLifecycle_MergeAbandonsRunningSLACycles(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	dup := env.newItem(t, env.workspaceA, "Dup")

	var metricID int
	if err := env.db.QueryRow(`
		INSERT INTO sla_metrics (workspace_id, name) VALUES (?, 'Lifecycle SLA') RETURNING id
	`, env.workspaceA).Scan(&metricID); err != nil {
		t.Fatalf("insert sla metric: %v", err)
	}
	if _, err := env.db.Exec(`
		INSERT INTO item_sla_cycles
			(item_id, metric_id, cycle_no, status, started_at, goal_duration_ms, elapsed_ms, remaining_ms,
			 last_calculated_at, next_deadline_at, calendar_snapshot, goal_query_snapshot)
		VALUES (?, ?, 1, 'ongoing', CURRENT_TIMESTAMP, 3600000, 0, 3600000,
		        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, '{}', '')
	`, dup, metricID); err != nil {
		t.Fatalf("insert sla cycle: %v", err)
	}

	if _, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dup}}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var status, reason string
	var stoppedAt, nextDeadline *time.Time
	if err := env.db.QueryRow(`
		SELECT status, COALESCE(abandon_reason, ''), stopped_at, next_deadline_at
		FROM item_sla_cycles WHERE item_id = ? AND metric_id = ?
	`, dup, metricID).Scan(&status, &reason, &stoppedAt, &nextDeadline); err != nil {
		t.Fatalf("read cycle: %v", err)
	}
	if status != models.SLACycleAbandoned {
		t.Fatalf("cycle status = %q, want abandoned", status)
	}
	if reason != "merged" {
		t.Fatalf("abandon_reason = %q, want merged", reason)
	}
	if stoppedAt == nil {
		t.Fatal("stopped_at is nil, want set")
	}
	if nextDeadline != nil {
		t.Fatalf("next_deadline_at = %v, want nil", nextDeadline)
	}
}

// TestItemLifecycle_MergeEmitsUpdatesForCanonicalAndSources verifies automations
// and webhooks learn about the merge from both sides: the canonical and each
// duplicate that now redirects to it (WI-1528).
func TestItemLifecycle_MergeEmitsUpdatesForCanonicalAndSources(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()
	emitter := &recordingLifecycleEmitter{}
	env.svc.SetEmitter(emitter)

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	dupA := env.newItem(t, env.workspaceA, "Dup A")
	dupB := env.newItem(t, env.workspaceA, "Dup B")

	if _, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dupA, dupB}}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	seen := map[int]bool{}
	for _, id := range emitter.updated {
		seen[id] = true
	}
	for _, want := range []int{canonical, dupA, dupB} {
		if !seen[want] {
			t.Fatalf("emitted updates = %v, missing item %d", emitter.updated, want)
		}
	}
}

// TestItemLifecycle_MergeIsIdempotent verifies re-merging the same duplicate
// into the same canonical is a no-op that still succeeds.
func TestItemLifecycle_MergeIsIdempotent(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	dup := env.newItem(t, env.workspaceA, "Dup")
	env.newComment(t, dup)

	input := ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dup}}
	if _, err := env.svc.Merge(ctx, input); err != nil {
		t.Fatalf("first Merge: %v", err)
	}
	result, err := env.svc.Merge(ctx, input)
	if err != nil {
		t.Fatalf("second Merge: %v", err)
	}
	if len(result.Sources) != 1 || result.Sources[0].Merged {
		t.Fatalf("replay result = %+v, want one skipped source", result.Sources)
	}
	if got := env.countDomainEvents(t, "item.merged"); got != 2 {
		t.Fatalf("item.merged facts after replay = %d, want 2 (no duplicates)", got)
	}
}

// TestItemLifecycle_MergeConflictWhenMergedElsewhere verifies a duplicate that
// already points at another canonical conflicts instead of re-pointing.
func TestItemLifecycle_MergeConflictWhenMergedElsewhere(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	other := env.newItem(t, env.workspaceA, "Other")
	dup := env.newItem(t, env.workspaceA, "Dup")

	if _, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dup}}); err != nil {
		t.Fatalf("first Merge: %v", err)
	}
	_, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: other, SourceItemIDs: []int{dup}})
	if !errors.Is(err, ErrItemMergeConflict) {
		t.Fatalf("err = %v, want ErrItemMergeConflict", err)
	}
}

// TestItemLifecycle_MergeRejectsDescendantTarget verifies a merge that would
// fold an item into its own descendant conflicts.
func TestItemLifecycle_MergeRejectsDescendantTarget(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	parent := env.newItem(t, env.workspaceA, "Parent")
	child := env.newItemWithParent(t, env.workspaceA, "Child", parent)

	_, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: child, SourceItemIDs: []int{parent}})
	if !errors.Is(err, ErrItemMergeConflict) {
		t.Fatalf("err = %v, want ErrItemMergeConflict", err)
	}
}

// TestItemLifecycle_MergeBlocksSubtickedDuplicates verifies a duplicate with
// children is refused rather than silently corrupting the hierarchy.
func TestItemLifecycle_MergeBlocksSubtickedDuplicates(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	dup := env.newItem(t, env.workspaceA, "Dup")
	env.newItemWithParent(t, env.workspaceA, "Child of dup", dup)

	_, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dup}})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want validation error", err)
	}
}

// TestItemLifecycle_MergeCrossWorkspaceRejected verifies duplicates from
// another workspace cannot be merged.
func TestItemLifecycle_MergeCrossWorkspaceRejected(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	foreign := env.newItem(t, env.workspaceB, "Foreign")

	_, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{foreign}})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want validation error", err)
	}
}

// TestItemLifecycle_MergeMakesCrossRequesterCommentsPrivate verifies that
// merging tickets with different requesters keeps customer content scoped:
// moved comments become private so the canonical's requester cannot read
// another customer's messages in the portal.
func TestItemLifecycle_MergeMakesCrossRequesterCommentsPrivate(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	requesterA := env.newPortalCustomer(t, "Requester A", "requester-a@example.com")
	requesterB := env.newPortalCustomer(t, "Requester B", "requester-b@example.com")

	canonical := env.newItemWithRequester(t, env.workspaceA, "Canonical", requesterA)
	dup := env.newItemWithRequester(t, env.workspaceA, "Dup", requesterB)
	env.newComment(t, dup)

	if _, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dup}}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var publicComments int
	if err := env.db.QueryRow(`
		SELECT COUNT(*) FROM comments WHERE item_id = ? AND (is_private = false OR is_private IS NULL)
	`, canonical).Scan(&publicComments); err != nil {
		t.Fatalf("count public comments: %v", err)
	}
	if publicComments != 0 {
		t.Fatalf("public comments on canonical = %d, want 0 (cross-requester content must be private)", publicComments)
	}
}

// TestItemLifecycle_MergeForbiddenWithoutEditPermission verifies the
// authorization contract: no item.edit on the workspace, no merge.
func TestItemLifecycle_MergeForbiddenWithoutEditPermission(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	dup := env.newItem(t, env.workspaceA, "Dup")

	svc := NewItemLifecycleService(env.db, nil) // nil permission service = no grants
	_, err := svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dup}})
	if !errors.Is(err, ErrItemForbidden) {
		t.Fatalf("err = %v, want ErrItemForbidden", err)
	}
	if merged := env.mergedInto(t, dup); merged != nil {
		t.Fatalf("forbidden merge still set pointer to %d", *merged)
	}
}

// TestItemLifecycle_SplitCreatesSubticketWithMovedContent verifies the split:
// a child ticket with explicit ownership receives exactly the selected
// comments and attachments; the rest stays on the source.
func TestItemLifecycle_SplitCreatesSubticketWithMovedContent(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	source := env.newItem(t, env.workspaceA, "Complex request")
	env.newComment(t, source) // stays on the source
	move1 := env.newComment(t, source)
	move2 := env.newComment(t, source)
	attachment := env.newAttachment(t, source)

	var assigneeID int
	if err := env.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&assigneeID); err != nil {
		t.Fatalf("load assignee: %v", err)
	}

	result, err := env.svc.Split(ctx, ItemSplitInput{
		ActorUserID: env.userID, ActorUsername: "agent",
		SourceItemID: source, Title: "Follow-up request",
		Description: "Split from the original request",
		CommentIDs:  []int{move1, move2}, AttachmentIDs: []int{attachment},
		AssigneeID: &assigneeID,
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	var parentID, assignee *int
	var title string
	if err := env.db.QueryRow(`SELECT parent_id, assignee_id, title FROM items WHERE id = ?`, result.SplitItemID).
		Scan(&parentID, &assignee, &title); err != nil {
		t.Fatalf("load split item: %v", err)
	}
	if parentID == nil || *parentID != source {
		t.Fatalf("split parent = %v, want %d", parentID, source)
	}
	if assignee == nil || *assignee != assigneeID {
		t.Fatalf("split assignee = %v, want %d (explicit agent ownership)", assignee, assigneeID)
	}
	if title != "Follow-up request" {
		t.Fatalf("split title = %q, want 'Follow-up request'", title)
	}

	var commentsOnChild, commentsOnSource, attachmentsOnChild int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, result.SplitItemID).Scan(&commentsOnChild); err != nil {
		t.Fatalf("count split comments: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, source).Scan(&commentsOnSource); err != nil {
		t.Fatalf("count source comments: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE item_id = ?`, result.SplitItemID).Scan(&attachmentsOnChild); err != nil {
		t.Fatalf("count split attachments: %v", err)
	}
	if commentsOnChild != 2 || commentsOnSource != 1 {
		t.Fatalf("comments child=%d source=%d, want 2/1", commentsOnChild, commentsOnSource)
	}
	if attachmentsOnChild != 1 {
		t.Fatalf("attachments on child = %d, want 1", attachmentsOnChild)
	}
	if result.MovedComments != 2 || result.MovedAttachments != 1 {
		t.Fatalf("result counts = %d/%d, want 2/1", result.MovedComments, result.MovedAttachments)
	}

	if got := env.countDomainEvents(t, "item.split"); got != 2 {
		t.Fatalf("item.split facts = %d, want 2", got)
	}
	if merged := env.mergedInto(t, source); merged != nil {
		t.Fatalf("split must not set the merge pointer, got %d", *merged)
	}
}

// TestItemLifecycle_SplitUsesNextHierarchyLevel verifies the split child gets
// the type one level below the source instead of copying the source type. A
// Story's subticket is a Task (or Bug), never another Story.
func TestItemLifecycle_SplitUsesNextHierarchyLevel(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	storyType := env.itemTypeIDByName(t, "Story")
	taskType := env.itemTypeIDByName(t, "Task")
	source := env.newItemWithType(t, env.workspaceA, "Story to split", storyType)
	comment := env.newComment(t, source)

	result, err := env.svc.Split(ctx, ItemSplitInput{
		ActorUserID: env.userID, SourceItemID: source, Title: "Follow-up task",
		CommentIDs: []int{comment},
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	var childType int
	if err := env.db.QueryRow(`SELECT item_type_id FROM items WHERE id = ?`, result.SplitItemID).Scan(&childType); err != nil {
		t.Fatalf("load split item type: %v", err)
	}
	if childType != taskType {
		t.Fatalf("split child type = %d, want next level %d (source was %d)", childType, taskType, storyType)
	}
}

// TestItemLifecycle_SplitHonorsExplicitChildType verifies a caller can pick a
// different legal child type when the parent has several (Bug alongside Task).
func TestItemLifecycle_SplitHonorsExplicitChildType(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	storyType := env.itemTypeIDByName(t, "Story")
	bugType := env.itemTypeIDByName(t, "Bug")
	source := env.newItemWithType(t, env.workspaceA, "Story to split", storyType)
	comment := env.newComment(t, source)

	result, err := env.svc.Split(ctx, ItemSplitInput{
		ActorUserID: env.userID, SourceItemID: source, Title: "Bug follow-up",
		ItemTypeID: &bugType,
		CommentIDs: []int{comment},
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	var childType int
	if err := env.db.QueryRow(`SELECT item_type_id FROM items WHERE id = ?`, result.SplitItemID).Scan(&childType); err != nil {
		t.Fatalf("load split item type: %v", err)
	}
	if childType != bugType {
		t.Fatalf("split child type = %d, want requested Bug %d", childType, bugType)
	}
}

// TestItemLifecycle_SplitRejectsSourceLevelType verifies the old behavior —
// reusing the source's own regular type — is now an explicit validation error
// rather than a silently invalid hierarchy.
func TestItemLifecycle_SplitRejectsSourceLevelType(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	storyType := env.itemTypeIDByName(t, "Story")
	source := env.newItemWithType(t, env.workspaceA, "Story to split", storyType)
	comment := env.newComment(t, source)

	_, err := env.svc.Split(ctx, ItemSplitInput{
		ActorUserID: env.userID, SourceItemID: source, Title: "Same type",
		ItemTypeID: &storyType,
		CommentIDs: []int{comment},
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want validation error", err)
	}
}

// TestItemLifecycle_SplitFallsBackToGenericSubtask verifies the lowest regular
// level has no next level, so the generic sub-task sentinel is used.
func TestItemLifecycle_SplitFallsBackToGenericSubtask(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	taskType := env.itemTypeIDByName(t, "Task")
	subtaskType := env.itemTypeIDByName(t, "Sub-task")
	source := env.newItemWithType(t, env.workspaceA, "Task to split", taskType)
	comment := env.newComment(t, source)

	result, err := env.svc.Split(ctx, ItemSplitInput{
		ActorUserID: env.userID, SourceItemID: source, Title: "Follow-up subtask",
		CommentIDs: []int{comment},
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	var childType int
	if err := env.db.QueryRow(`SELECT item_type_id FROM items WHERE id = ?`, result.SplitItemID).Scan(&childType); err != nil {
		t.Fatalf("load split item type: %v", err)
	}
	if childType != subtaskType {
		t.Fatalf("split child type = %d, want generic Sub-task %d", childType, subtaskType)
	}
}

// TestItemLifecycle_SplitRejectsGenericSubtaskSource verifies generic
// sub-tasks are terminal, so they cannot be split further.
func TestItemLifecycle_SplitRejectsGenericSubtaskSource(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	subtaskType := env.itemTypeIDByName(t, "Sub-task")
	source := env.newItemWithType(t, env.workspaceA, "Sub-task to split", subtaskType)
	comment := env.newComment(t, source)

	_, err := env.svc.Split(ctx, ItemSplitInput{
		ActorUserID: env.userID, SourceItemID: source, Title: "Nested subtask",
		CommentIDs: []int{comment},
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want validation error", err)
	}
}

// TestItemLifecycle_MergeLeavesPageAttachmentsAlone verifies WI-1565: the
// lifecycle move is item-scoped, so a page whose numeric id collides with the
// ticket id keeps its own files when the ticket is merged.
func TestItemLifecycle_MergeLeavesPageAttachmentsAlone(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	dup := env.newItem(t, env.workspaceA, "Dup")

	// A page whose numeric id equals the duplicate's id, with its own file.
	if _, err := env.db.Exec(`
		INSERT INTO pages (id, workspace_id, title, slug, created_by) VALUES (?, ?, 'Colliding page', 'colliding-page', ?)
	`, dup, env.workspaceA, env.userID); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	var pageAttachmentID int
	if err := env.db.QueryRow(`
		INSERT INTO attachments (item_id, filename, original_filename, file_path, mime_type, file_size, entity_type, created_at)
		VALUES (?, 'page.bin', 'page.bin', 'pages/page.bin', 'application/octet-stream', 1, 'page', CURRENT_TIMESTAMP)
		RETURNING id
	`, dup).Scan(&pageAttachmentID); err != nil {
		t.Fatalf("insert page attachment: %v", err)
	}
	itemAttachmentID := env.newAttachment(t, dup)

	if _, err := env.svc.Merge(ctx, ItemMergeInput{ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{dup}}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var pageItemID int
	var pageEntity string
	if err := env.db.QueryRow(`SELECT item_id, COALESCE(entity_type, '') FROM attachments WHERE id = ?`, pageAttachmentID).Scan(&pageItemID, &pageEntity); err != nil {
		t.Fatalf("read page attachment: %v", err)
	}
	if pageItemID != dup || pageEntity != "page" {
		t.Fatalf("page attachment = (item_id %d, entity_type %q), want (%d, page)", pageItemID, pageEntity, dup)
	}

	var itemAttachmentOnCanonical int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id = ? AND item_id = ?`, itemAttachmentID, canonical).Scan(&itemAttachmentOnCanonical); err != nil {
		t.Fatalf("read item attachment: %v", err)
	}
	if itemAttachmentOnCanonical != 1 {
		t.Fatal("item attachment did not follow the canonical")
	}
}

// TestItemLifecycle_SplitKeepsSourceChannel verifies WI-1568: a customer-facing
// split keeps the channel provenance, so the child stays reachable in the
// portal instead of becoming a channel-less, invisible ticket.
func TestItemLifecycle_SplitKeepsSourceChannel(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	var channelID int
	if err := env.db.QueryRow(`
		INSERT INTO channels (name, type, direction, status) VALUES ('Portal', 'portal', 'inbound', 'enabled') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	source := env.newItem(t, env.workspaceA, "Portal request")
	if _, err := env.db.Exec(`UPDATE items SET channel_id = ? WHERE id = ?`, channelID, source); err != nil {
		t.Fatalf("set source channel: %v", err)
	}
	comment := env.newComment(t, source)

	result, err := env.svc.Split(ctx, ItemSplitInput{
		ActorUserID: env.userID, ActorUsername: "agent",
		SourceItemID: source, Title: "Follow-up",
		CommentIDs: []int{comment},
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	var childChannel *int
	if err := env.db.QueryRow(`SELECT channel_id FROM items WHERE id = ?`, result.SplitItemID).Scan(&childChannel); err != nil {
		t.Fatalf("load split channel: %v", err)
	}
	if childChannel == nil || *childChannel != channelID {
		t.Fatalf("split channel = %v, want %d", childChannel, channelID)
	}
}

// TestItemLifecycle_SplitValidatesSelectionOwnership verifies the split only
// moves content that belongs to the source item.
func TestItemLifecycle_SplitValidatesSelectionOwnership(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	source := env.newItem(t, env.workspaceA, "Source")
	other := env.newItem(t, env.workspaceA, "Other")
	foreignComment := env.newComment(t, other)

	_, err := env.svc.Split(ctx, ItemSplitInput{
		ActorUserID: env.userID, SourceItemID: source, Title: "Split",
		CommentIDs: []int{foreignComment},
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want validation error", err)
	}
}

// TestItemLifecycle_SplitRequiresContent verifies splitting nothing away is
// rejected up front.
func TestItemLifecycle_SplitRequiresContent(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	ctx := context.Background()

	source := env.newItem(t, env.workspaceA, "Source")
	_, err := env.svc.Split(ctx, ItemSplitInput{ActorUserID: env.userID, SourceItemID: source, Title: "Empty split"})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want validation error", err)
	}
}
