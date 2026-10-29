//go:build test

package services

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// auditRowsFor counts committed audit rows for one action type.
func auditRowsFor(t *testing.T, env *itemSupportEventsEnv, actionType string) int {
	t.Helper()
	var count int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ?`, actionType).Scan(&count); err != nil {
		t.Fatalf("count audit rows for %s: %v", actionType, err)
	}
	return count
}

func createSecondTicket(t *testing.T, env *itemSupportEventsEnv, title string) int {
	t.Helper()
	created, err := CreateItem(env.db, ItemCreationParams{
		WorkspaceID: env.workspaceID,
		ItemTypeID:  &env.itemTypeID,
		Title:       title,
		StatusID:    &env.openStatus,
		CreatorID:   &env.actorUserID,
		ChannelID:   &env.channelID,
	})
	if err != nil {
		t.Fatalf("create ticket %s: %v", title, err)
	}
	return int(created)
}

// TestAuditMergeAndReplyAreRecorded pins the WI-1142 audit contract: agent
// replies on customer tickets, ticket merges, and ticket assignments leave
// central audit rows, while private notes, internal items, and internal-item
// assignments do not.
func TestAuditMergeAndReplyAreRecorded(t *testing.T) {
	env := newItemSupportEventsEnv(t)

	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, AuthorID: env.authorID, Content: "public reply", ActorUserID: env.actorUserID,
	}); err != nil {
		t.Fatalf("public reply: %v", err)
	}
	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, AuthorID: env.authorID, Content: "note", IsPrivate: true, ActorUserID: env.actorUserID,
	}); err != nil {
		t.Fatalf("private note: %v", err)
	}
	if got := auditRowsFor(t, env, logger.ActionCommentCreate); got != 1 {
		t.Fatalf("comment.create rows = %d, want 1 (private note excluded)", got)
	}

	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.internalID, AuthorID: env.authorID, Content: "internal public", ActorUserID: env.actorUserID,
	}); err != nil {
		t.Fatalf("internal comment: %v", err)
	}
	if got := auditRowsFor(t, env, logger.ActionCommentCreate); got != 1 {
		t.Fatalf("comment.create rows after internal comment = %d, want 1", got)
	}

	// Assignment on the ticket: audited with old and new ids.
	newAssignee := testutils.InsertID(t, env.db,
		`INSERT INTO users (email, username, first_name, last_name) VALUES ('assign-audit@test.local', 'assign-audit', 'A', 'U')`)
	if _, err := NewItemUpdateService(env.db).UpdateItem(UpdateItemRequest{
		ItemID:     env.ticketID,
		UpdateData: map[string]any{"assignee_id": newAssignee},
		UserID:     env.actorUserID,
	}); err != nil {
		t.Fatalf("ticket assignment: %v", err)
	}
	if got := auditRowsFor(t, env, logger.ActionItemAssign); got != 1 {
		t.Fatalf("item.assign rows = %d, want 1", got)
	}

	// Assignment on the internal item: not audited.
	if _, err := NewItemUpdateService(env.db).UpdateItem(UpdateItemRequest{
		ItemID:     env.internalID,
		UpdateData: map[string]any{"assignee_id": newAssignee},
		UserID:     env.actorUserID,
	}); err != nil {
		t.Fatalf("internal assignment: %v", err)
	}
	if got := auditRowsFor(t, env, logger.ActionItemAssign); got != 1 {
		t.Fatalf("item.assign rows after internal assignment = %d, want 1", got)
	}

	// Merge a duplicate into the canonical ticket: audited with source and
	// target.
	permissions, permErr := NewPermissionService(env.db, DefaultPermissionCacheConfig())
	if permErr != nil {
		t.Fatalf("permission service: %v", permErr)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	lifecycle := NewItemLifecycleService(env.db, permissions)
	second := createSecondTicket(t, env, "Duplicate ticket")
	if _, err := lifecycle.Merge(context.Background(), ItemMergeInput{
		ActorUserID:   env.actorUserID,
		ActorUsername: "audit-tester",
		TargetItemID:  env.ticketID,
		SourceItemIDs: []int{second},
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := auditRowsFor(t, env, logger.ActionItemMerge); got != 1 {
		t.Fatalf("item.merge rows = %d, want 1", got)
	}
	var details string
	if err := env.db.QueryRow(`SELECT details FROM audit_logs WHERE action_type = ?`, logger.ActionItemMerge).Scan(&details); err != nil {
		t.Fatalf("load merge audit row: %v", err)
	}
	if !strings.Contains(details, fmt.Sprint(second)) || !strings.Contains(details, fmt.Sprint(env.ticketID)) {
		t.Errorf("merge audit details = %s, want source %d and target %d", details, second, env.ticketID)
	}
}

// TestAuditSplitIsRecorded covers the split side of the lifecycle audit.
func TestAuditSplitIsRecorded(t *testing.T) {
	env := newItemSupportEventsEnv(t)
	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, AuthorID: env.authorID, Content: "moved out", ActorUserID: env.actorUserID,
	}); err != nil {
		t.Fatalf("comment: %v", err)
	}

	permissions, permErr := NewPermissionService(env.db, DefaultPermissionCacheConfig())
	if permErr != nil {
		t.Fatalf("permission service: %v", permErr)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	// A real comment id is required: split moves selected content out.
	var commentID int
	if err := env.db.QueryRow(`SELECT id FROM comments WHERE item_id = ? ORDER BY id LIMIT 1`, env.ticketID).Scan(&commentID); err != nil {
		t.Fatalf("load comment for split: %v", err)
	}
	lifecycle := NewItemLifecycleService(env.db, permissions)
	result, err := lifecycle.Split(context.Background(), ItemSplitInput{
		ActorUserID:   env.actorUserID,
		ActorUsername: "audit-tester",
		SourceItemID:  env.ticketID,
		Title:         "Split child",
		CommentIDs:    []int{commentID},
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if got := auditRowsFor(t, env, logger.ActionItemSplit); got != 1 {
		t.Fatalf("item.split rows = %d, want 1", got)
	}
	var resourceID int
	if err := env.db.QueryRow(`SELECT resource_id FROM audit_logs WHERE action_type = ?`, logger.ActionItemSplit).Scan(&resourceID); err != nil {
		t.Fatalf("load split audit row: %v", err)
	}
	if resourceID != result.SplitItemID {
		t.Errorf("split audit resource_id = %d, want child %d", resourceID, result.SplitItemID)
	}
}

// TestAutomationCommentsDoNotRetrigger pins the WI-1142 loop guard: comments
// authored by automation never re-enter the comment_created trigger.
func TestAutomationCommentsDoNotRetrigger(t *testing.T) {
	env := newItemSupportEventsEnv(t)
	emitter := &stubRecordingEmitter{}
	env.comments.SetActionEventEmitter(emitter)

	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, AuthorID: env.authorID, Content: "human reply", ActorUserID: env.authorID,
	}); err != nil {
		t.Fatalf("human comment: %v", err)
	}
	if emitter.count != 1 {
		t.Fatalf("emitted events after human comment = %d, want 1", emitter.count)
	}

	if _, err := env.comments.Create(CreateCommentParams{
		ItemID: env.ticketID, AuthorID: env.authorID, Content: "automation reply",
		ActorUserID: env.actorUserID, SuppressActionEvents: true,
	}); err != nil {
		t.Fatalf("automation comment: %v", err)
	}
	if emitter.count != 1 {
		t.Fatalf("emitted events after suppressed automation comment = %d, want 1", emitter.count)
	}
}

type stubRecordingEmitter struct {
	count int
}

func (s *stubRecordingEmitter) EmitActionEvent(event *models.ActionEvent) {
	s.count++
}
