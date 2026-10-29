//go:build test

package email

import (
	"context"
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// TestFindParentItemRoutesReplyAfterMerge verifies a reply to a merged
// duplicate's thread still reaches the canonical: the merge re-points
// email_message_tracking, so matching happens before the read-only redirect
// would refuse the comment.
func TestFindParentItemRoutesReplyAfterMerge(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var channelID int
	if err := db.QueryRow(`INSERT INTO channels (name, type, direction) VALUES ('Mailbox', 'email', 'inbound') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`INSERT INTO portal_customers (name, email) VALUES ('Customer', 'customer@example.com') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}

	newItem := func(number int, title string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, channel_id, creator_portal_customer_id, frac_index)
			VALUES (?, ?, ?, '', ?, ?, ?) RETURNING id
		`, data.WorkspaceID, number, title, channelID, customerID, testutils.NextTestFracIndex()).Scan(&id); err != nil {
			t.Fatalf("insert item %q: %v", title, err)
		}
		return id
	}
	canonical := newItem(1, "Canonical")
	duplicate := newItem(2, "Duplicate")

	const threadMessageID = "<thread-dup@example.com>"
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking (channel_id, message_id, dedup_key, from_email, item_id)
		VALUES (?, ?, ?, 'customer@example.com', ?)
	`, channelID, threadMessageID, threadMessageID, duplicate); err != nil {
		t.Fatalf("insert tracking: %v", err)
	}

	perm, err := services.NewPermissionService(db, services.PermissionCacheConfig{TTL: time.Minute, MaxCacheSize: 8, BatchSize: 10})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = perm.Close() })
	lifecycle := services.NewItemLifecycleService(db, perm)
	if _, err := lifecycle.Merge(context.Background(), services.ItemMergeInput{
		ActorUserID: data.UserID, TargetItemID: canonical, SourceItemIDs: []int{duplicate},
	}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	parent := NewProcessor(db, blobstore.Disabled()).findParentItem(context.Background(), &ParsedEmail{
		InReplyTo: threadMessageID,
		From:      EmailAddress{Address: "customer@example.com"},
	})
	if parent == nil || *parent != canonical {
		t.Fatalf("reply after merge parent = %v, want canonical %d", parent, canonical)
	}
}
