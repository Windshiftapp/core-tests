//go:build test

package services_test

import (
	"context"
	"testing"

	"windshift/internal/services"
)

// recordingMentionTrigger captures MaybeStartRunsForMentions invocations.
type recordingMentionTrigger struct {
	calls       int
	userIDs     []int
	authorID    int
	itemID      int
	workspace   int
	commentBody string
	commentID   int
}

func (r *recordingMentionTrigger) MaybeStartRunsForMentions(_ context.Context, workspaceID, itemID int, mentionedUserIDs []int, commentAuthorID int, commentBody string, commentID int) error {
	r.calls++
	r.workspace = workspaceID
	r.itemID = itemID
	r.userIDs = mentionedUserIDs
	r.authorID = commentAuthorID
	r.commentBody = commentBody
	r.commentID = commentID
	return nil
}

// TestCommentService_AgentMentionTrigger pins the WI-264 wiring contract:
// creating a comment that @mentions a user invokes the agent-mention
// trigger with the resolved ids and the author as principal; editing a
// comment — even one that adds a brand-new mention — never re-triggers.
func TestCommentService_AgentMentionTrigger(t *testing.T) {
	db := createCommentTestDB(t)
	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	var username string
	if err := db.QueryRow(`SELECT username FROM users WHERE id = ?`, env.UserID).Scan(&username); err != nil {
		t.Fatalf("read username: %v", err)
	}

	trigger := &recordingMentionTrigger{}
	service.SetMentionService(services.NewMentionService(db, nil, nil))
	service.SetAgentMentionTrigger(trigger)

	created, err := service.Create(services.CreateCommentParams{
		ItemID:      env.ItemID,
		AuthorID:    env.UserID,
		Content:     "hey @" + username + " take a look",
		ActorUserID: env.UserID,
	})
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	if trigger.calls != 1 {
		t.Fatalf("trigger calls after create: want 1, got %d", trigger.calls)
	}
	if len(trigger.userIDs) != 1 || trigger.userIDs[0] != env.UserID {
		t.Errorf("mentioned ids: want [%d], got %v", env.UserID, trigger.userIDs)
	}
	if trigger.authorID != env.UserID || trigger.itemID != env.ItemID || trigger.workspace != env.WorkspaceID {
		t.Errorf("trigger context: got author=%d item=%d ws=%d", trigger.authorID, trigger.itemID, trigger.workspace)
	}
	// The comment body + id ride along so the trigger can persist the comment
	// as the run's instruction (not just fire-and-forget on the mention).
	if trigger.commentBody != "hey @"+username+" take a look" {
		t.Errorf("comment body threaded to trigger: got %q", trigger.commentBody)
	}
	if trigger.commentID != int(created.CommentID) {
		t.Errorf("comment id threaded to trigger: want %d, got %d", int(created.CommentID), trigger.commentID)
	}

	// Editing the comment to add a mention must NOT re-trigger.
	if _, err := service.Update(int(created.CommentID), "now also @"+username+" again, edited", env.UserID); err != nil {
		t.Fatalf("update comment: %v", err)
	}
	if trigger.calls != 1 {
		t.Errorf("trigger calls after edit: want still 1, got %d", trigger.calls)
	}

	// A comment without mentions never invokes the trigger.
	if _, err := service.Create(services.CreateCommentParams{
		ItemID:      env.ItemID,
		AuthorID:    env.UserID,
		Content:     "no mentions here",
		ActorUserID: env.UserID,
	}); err != nil {
		t.Fatalf("create plain comment: %v", err)
	}
	if trigger.calls != 1 {
		t.Errorf("trigger calls after mention-less comment: want still 1, got %d", trigger.calls)
	}

	// Suppressed comments (plugin/automation-created) skip the trigger.
	if _, err := service.Create(services.CreateCommentParams{
		ItemID:                env.ItemID,
		AuthorID:              env.UserID,
		Content:               "automation says @" + username,
		ActorUserID:           env.UserID,
		SuppressNotifications: true,
	}); err != nil {
		t.Fatalf("create suppressed comment: %v", err)
	}
	if trigger.calls != 1 {
		t.Errorf("trigger calls after suppressed comment: want still 1, got %d", trigger.calls)
	}
}

// The trigger receives the same source users see and edit, so its instruction
// cannot diverge from the persisted comment.
func TestCommentService_AgentMentionTrigger_PreservesInstructionSource(t *testing.T) {
	db := createCommentTestDB(t)
	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	var username string
	if err := db.QueryRow(`SELECT username FROM users WHERE id = ?`, env.UserID).Scan(&username); err != nil {
		t.Fatalf("read username: %v", err)
	}

	trigger := &recordingMentionTrigger{}
	service.SetMentionService(services.NewMentionService(db, nil, nil))
	service.SetAgentMentionTrigger(trigger)

	raw := "hey @" + username + " <script>alert('xss')</script> see [x](javascript:alert(1))"
	if _, err := service.Create(services.CreateCommentParams{
		ItemID:      env.ItemID,
		AuthorID:    env.UserID,
		Content:     raw,
		ActorUserID: env.UserID,
	}); err != nil {
		t.Fatalf("create comment: %v", err)
	}
	if trigger.calls != 1 {
		t.Fatalf("trigger calls: want 1, got %d", trigger.calls)
	}
	if trigger.commentBody != raw {
		t.Errorf("trigger instruction = %q, want exact source %q", trigger.commentBody, raw)
	}
	if len(trigger.userIDs) != 1 || trigger.userIDs[0] != env.UserID {
		t.Errorf("mention resolution broke for source content: %v", trigger.userIDs)
	}
}
