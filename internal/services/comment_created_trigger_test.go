//go:build test

package services

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

type commentTriggerFixture struct {
	db          database.Database
	comments    *CommentService
	emitter     *stubNodeAPI
	workspaceID int
	itemID      int
	authorID    int
}

func newCommentTriggerFixture(t *testing.T) *commentTriggerFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var userID int
	if err := db.QueryRow(`INSERT INTO users (email, username, first_name, last_name) VALUES ('trigger@test.local', 'trigger-agent', 'T', 'Agent') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Trigger WS', 'TRIG') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, creator_id) VALUES (?, 9, 'Triggered ticket', 'd', ?, ?) RETURNING id`,
		workspaceID, testutils.NextTestFracIndex(), userID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	comments := NewCommentService(db)
	emitter := &stubNodeAPI{}
	comments.SetActionEventEmitter(emitter)
	return &commentTriggerFixture{db: db, comments: comments, emitter: emitter, workspaceID: workspaceID, itemID: itemID, authorID: userID}
}

func TestCommentCreateEmitsAutomationEventForAgent(t *testing.T) {
	f := newCommentTriggerFixture(t)
	result, err := f.comments.Create(CreateCommentParams{
		ItemID:      f.itemID,
		AuthorID:    f.authorID,
		Content:     "agent reply",
		ActorUserID: f.authorID,
	})
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	if len(f.emitter.emitted) != 1 {
		t.Fatalf("emitted %d events, want 1", len(f.emitter.emitted))
	}
	event := f.emitter.emitted[0]
	if event.EventType != models.ActionTriggerCommentCreated {
		t.Fatalf("event type = %s, want comment_created", event.EventType)
	}
	if event.ItemID != f.itemID || event.WorkspaceID != f.workspaceID {
		t.Fatalf("event item=%d ws=%d, want %d/%d", event.ItemID, event.WorkspaceID, f.itemID, f.workspaceID)
	}
	if event.NewValues["comment_id"] != int(result.CommentID) {
		t.Fatalf("comment_id = %v, want %d", event.NewValues["comment_id"], result.CommentID)
	}
	if event.NewValues["comment_from_customer"] != false {
		t.Fatalf("comment_from_customer = %v, want false for an agent comment", event.NewValues["comment_from_customer"])
	}
	if event.NewValues["comment_is_private"] != false {
		t.Fatalf("comment_is_private = %v, want false", event.NewValues["comment_is_private"])
	}
}

func TestCommentCreateEmitsAutomationEventForCustomerAndPrivacy(t *testing.T) {
	f := newCommentTriggerFixture(t)

	var customerID int
	if err := f.db.QueryRow(`INSERT INTO portal_customers (email, name) VALUES ('cust@example.test', 'Pat Customer') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}

	if _, err := f.comments.Create(CreateCommentParams{
		ItemID:           f.itemID,
		Content:          "customer question",
		PortalCustomerID: &customerID,
	}); err != nil {
		t.Fatalf("create customer comment: %v", err)
	}
	if len(f.emitter.emitted) != 1 {
		t.Fatalf("emitted %d events, want 1", len(f.emitter.emitted))
	}
	if f.emitter.emitted[0].NewValues["comment_from_customer"] != true {
		t.Fatalf("comment_from_customer = %v, want true for a portal-customer comment", f.emitter.emitted[0].NewValues["comment_from_customer"])
	}

	// Private agent comments still fire (agents can automate on internal
	// notes) but carry the privacy flag so customer-facing nodes can guard.
	if _, err := f.comments.Create(CreateCommentParams{
		ItemID:      f.itemID,
		AuthorID:    f.authorID,
		Content:     "internal note",
		IsPrivate:   true,
		ActorUserID: f.authorID,
	}); err != nil {
		t.Fatalf("create private comment: %v", err)
	}
	if len(f.emitter.emitted) != 2 {
		t.Fatalf("emitted %d events, want 2", len(f.emitter.emitted))
	}
	if f.emitter.emitted[1].NewValues["comment_is_private"] != true {
		t.Fatalf("comment_is_private = %v, want true", f.emitter.emitted[1].NewValues["comment_is_private"])
	}
}
