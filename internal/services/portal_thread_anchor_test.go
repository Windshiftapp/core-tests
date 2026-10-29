//go:build test

package services

import (
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// seedAnchorChannel inserts a channel and returns its id. RETURNING works on
// both engines, unlike LastInsertId (Postgres).
func seedAnchorChannel(t *testing.T, db database.Database, name, channelType, direction string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(
		"INSERT INTO channels (name, type, direction, status) VALUES (?, ?, ?, 'disabled') RETURNING id",
		name, channelType, direction,
	).Scan(&id); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return id
}

// seedAnchorCustomer inserts a portal customer and returns its id.
func seedAnchorCustomer(t *testing.T, db database.Database, email string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(
		"INSERT INTO portal_customers (name, email, created_via) VALUES ('Customer', ?, 'agent') RETURNING id",
		email,
	).Scan(&id); err != nil {
		t.Fatalf("seed portal customer: %v", err)
	}
	return id
}

// anchorRows returns the tracking rows recorded for an item.
func anchorRows(t *testing.T, db database.Database, itemID int64) []map[string]any {
	t.Helper()
	rows, err := db.Query(`
		SELECT channel_id, message_id, dedup_key, direction,
		       COALESCE(from_email, ''), subject, comment_id
		FROM email_message_tracking
		WHERE item_id = ?
		ORDER BY id
	`, itemID)
	if err != nil {
		t.Fatalf("query anchors: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []map[string]any
	for rows.Next() {
		var channelID int
		var messageID, dedupKey, direction, fromEmail string
		var subject *string
		var commentID *int
		if err := rows.Scan(&channelID, &messageID, &dedupKey, &direction, &fromEmail, &subject, &commentID); err != nil {
			t.Fatalf("scan anchor row: %v", err)
		}
		out = append(out, map[string]any{
			"channel_id": channelID,
			"message_id": messageID,
			"dedup_key":  dedupKey,
			"direction":  direction,
			"from_email": fromEmail,
			"subject":    subject,
			"comment_id": commentID,
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate anchors: %v", err)
	}
	return out
}

// createAnchoredItem runs CreateItem with the given channel and customer and
// returns the new item id. Either pointer may be nil.
func createAnchoredItem(t *testing.T, db database.Database, workspaceID int, channelID, customerID *int, title string) int64 {
	t.Helper()
	var itemTypeID int
	if err := db.QueryRow("SELECT id FROM item_types ORDER BY id LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("get item type: %v", err)
	}
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:             workspaceID,
		Title:                   title,
		ItemTypeID:              &itemTypeID,
		ChannelID:               channelID,
		CreatorPortalCustomerID: customerID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	return itemID
}

// TestCreateItem_PortalThreadAnchorByChannelType covers which creation shapes
// mint a synthetic thread root: portal/form submissions by a portal customer
// do, email-intake items and creations without a portal-customer creator do
// not (email items already anchor to their real inbound Message-ID).
func TestCreateItem_PortalThreadAnchorByChannelType(t *testing.T) {
	cases := []struct {
		name        string
		channelType string
		wantAnchor  bool
	}{
		{"portal channel anchors", "portal", true},
		{"form channel anchors", "form", true},
		{"email channel skipped", "email", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, false)
			data := tdb.SeedTestData(t)
			db := tdb.GetDatabase()

			channelID := seedAnchorChannel(t, db, "ch-"+tc.channelType, tc.channelType, "inbound")
			customerID := seedAnchorCustomer(t, db, fmt.Sprintf("cust-%s@example.com", tc.channelType))
			title := "Broken login"

			itemID := createAnchoredItem(t, db, data.WorkspaceID, &channelID, &customerID, title)
			rows := anchorRows(t, db, itemID)

			if !tc.wantAnchor {
				if len(rows) != 0 {
					t.Fatalf("expected no anchor rows for %s channel, got %d", tc.channelType, len(rows))
				}
				return
			}

			if len(rows) != 1 {
				t.Fatalf("expected exactly 1 anchor row, got %d", len(rows))
			}
			row := rows[0]
			wantID := fmt.Sprintf("<ws-item-%d@windshift.local>", itemID)
			if row["message_id"] != wantID {
				t.Fatalf("message_id = %v, want %v", row["message_id"], wantID)
			}
			if row["dedup_key"] != wantID {
				t.Fatalf("dedup_key = %v, want %v (anchor is its own dedup key)", row["dedup_key"], wantID)
			}
			if row["direction"] != "outbound" {
				t.Fatalf("direction = %v, want outbound", row["direction"])
			}
			if row["from_email"] != "" {
				t.Fatalf("from_email = %q, want empty (anchor must not grant thread participation)", row["from_email"])
			}
			if row["comment_id"] != (*int)(nil) {
				t.Fatalf("comment_id = %v, want NULL for a root anchor", row["comment_id"])
			}
			if row["channel_id"] != channelID {
				t.Fatalf("channel_id = %v, want %d (origin channel)", row["channel_id"], channelID)
			}
			subject, _ := row["subject"].(*string)
			if subject == nil || *subject != title {
				t.Fatalf("subject = %v, want %q", row["subject"], title)
			}
		})
	}
}

// TestCreateItem_PortalThreadAnchor_RequiresCustomer verifies an item on a
// portal channel created by an internal user (no portal customer) gets no
// anchor: there is nobody to email.
func TestCreateItem_PortalThreadAnchor_RequiresCustomer(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	channelID := seedAnchorChannel(t, db, "portal-no-customer", "portal", "inbound")
	itemID := createAnchoredItem(t, db, data.WorkspaceID, &channelID, nil, "internal request")

	if rows := anchorRows(t, db, itemID); len(rows) != 0 {
		t.Fatalf("expected no anchor without a portal-customer creator, got %d rows", len(rows))
	}
}

// TestPortalThreadAnchor_Idempotent verifies re-recording the anchor for the
// same item is a no-op via the (channel_id, dedup_key) unique index.
func TestPortalThreadAnchor_Idempotent(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	channelID := seedAnchorChannel(t, db, "portal-idem", "portal", "inbound")
	customerID := seedAnchorCustomer(t, db, "idem@example.com")
	itemID := createAnchoredItem(t, db, data.WorkspaceID, &channelID, &customerID, "idem item")

	maybeRecordPortalThreadAnchor(db, itemID, &channelID, &customerID, "idem item")
	maybeRecordPortalThreadAnchor(db, itemID, &channelID, &customerID, "idem item")

	if rows := anchorRows(t, db, itemID); len(rows) != 1 {
		t.Fatalf("expected 1 anchor after re-recording, got %d", len(rows))
	}
}

// TestCreateItem_PortalThreadAnchor_UsesSMTPDomain verifies the anchor
// Message-ID derives its domain from the default outbound SMTP channel so
// replies address the same domain the notifications were sent from.
func TestCreateItem_PortalThreadAnchor_UsesSMTPDomain(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	// The test database seeds a default outbound SMTP channel; point it at the
	// domain under test rather than fighting the is_default unique index.
	if _, err := db.ExecWrite(`
		UPDATE channels
		SET config = '{"smtp_from_email":"support@example.com"}', status = 'enabled', is_default = TRUE
		WHERE type = 'smtp' AND direction = 'outbound'
	`); err != nil {
		t.Fatalf("seed smtp channel: %v", err)
	}

	channelID := seedAnchorChannel(t, db, "portal-smtp", "portal", "inbound")
	customerID := seedAnchorCustomer(t, db, "smtpdom@example.com")
	itemID := createAnchoredItem(t, db, data.WorkspaceID, &channelID, &customerID, "smtp domain")

	rows := anchorRows(t, db, itemID)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 anchor row, got %d", len(rows))
	}
	wantID := fmt.Sprintf("<ws-item-%d@example.com>", itemID)
	if rows[0]["message_id"] != wantID {
		t.Fatalf("message_id = %v, want %v", rows[0]["message_id"], wantID)
	}
}
