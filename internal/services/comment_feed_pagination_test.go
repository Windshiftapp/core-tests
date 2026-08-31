//go:build test

package services_test

import (
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

type commentFeedEnv struct {
	db     database.Database
	itemID int
	userID int
	status int
}

func newCommentFeedEnv(t *testing.T) commentFeedEnv {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	f := factory.NewTestFactory(tdb.GetDatabase())
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Comment feed pagination",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	return commentFeedEnv{
		db:     tdb.GetDatabase(),
		itemID: itemID,
		userID: data.UserID,
		status: data.StatusID,
	}
}

func (env commentFeedEnv) addApprovalFeedRow(t *testing.T, content string, createdAt time.Time) {
	env.addApprovalFeedRowAs(t, content, createdAt, env.userID)
}

func (env commentFeedEnv) addApprovalFeedRowAs(t *testing.T, content string, createdAt time.Time, actorUserID int) {
	t.Helper()
	var workflowID int
	if err := env.db.QueryRow(`
		INSERT INTO workflows (name, description, is_default)
		VALUES ('comment-feed-pagination', '', false) RETURNING id
	`).Scan(&workflowID); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	var transitionID int
	if err := env.db.QueryRow(`
		INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id)
		VALUES (?, ?, ?) RETURNING id
	`, workflowID, env.status, env.status).Scan(&transitionID); err != nil {
		t.Fatalf("insert transition: %v", err)
	}
	var approvalSetID int
	if err := env.db.QueryRow(`
		INSERT INTO approval_sets (name, workflow_id)
		VALUES ('comment-feed-pagination', ?) RETURNING id
	`, workflowID).Scan(&approvalSetID); err != nil {
		t.Fatalf("insert approval set: %v", err)
	}
	var approvalSetStatusID int
	if err := env.db.QueryRow(`
		INSERT INTO approval_set_statuses
			(approval_set_id, status_id, approve_transition_id, deny_transition_id)
		VALUES (?, ?, ?, ?) RETURNING id
	`, approvalSetID, env.status, transitionID, transitionID).Scan(&approvalSetStatusID); err != nil {
		t.Fatalf("insert approval set status: %v", err)
	}
	var requestID int
	if err := env.db.QueryRow(`
		INSERT INTO approval_requests
			(item_id, approval_set_status_id, status_id, triggered_by_user_id, status)
		VALUES (?, ?, ?, ?, 'approved') RETURNING id
	`, env.itemID, approvalSetStatusID, env.status, env.userID).Scan(&requestID); err != nil {
		t.Fatalf("insert approval request: %v", err)
	}
	if _, err := env.db.ExecWrite(`
		INSERT INTO approval_decisions
			(approval_request_id, actor_user_id, decision, comment, created_at)
		VALUES (?, ?, 'approve', ?, ?)
	`, requestID, actorUserID, content, createdAt); err != nil {
		t.Fatalf("insert approval decision: %v", err)
	}
}

func TestCommentService_GetFeedByItemIDFiltersApprovalAgentOwnerAttribution(t *testing.T) {
	env := newCommentFeedEnv(t)
	var ownerID int
	if err := env.db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('approval-owner@example.test', 'approval-owner', 'Approval', 'Owner')
		RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	var agentID int
	if err := env.db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_agent, agent_owner_user_id)
		VALUES ('approval-agent@example.test', 'approval-agent', 'Approval', 'Agent', true, ?)
		RETURNING id
	`, ownerID).Scan(&agentID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	env.addApprovalFeedRowAs(t, "agent approval", time.Date(2026, time.July, 27, 11, 0, 0, 0, time.UTC), agentID)

	service := services.NewCommentService(env.db)
	withoutOwner, err := service.GetFeedByItemID(env.itemID, false, services.CommentFeedOptions{})
	if err != nil {
		t.Fatalf("get feed without owner attribution: %v", err)
	}
	if len(withoutOwner.Comments) != 1 || withoutOwner.Comments[0].AgentOwnerName != "" {
		t.Fatalf("feed without owner attribution = %+v, want owner omitted", withoutOwner.Comments)
	}

	withOwner, err := service.GetFeedByItemID(env.itemID, true, services.CommentFeedOptions{})
	if err != nil {
		t.Fatalf("get feed with owner attribution: %v", err)
	}
	if len(withOwner.Comments) != 1 || withOwner.Comments[0].AgentOwnerName != "Approval Owner" {
		t.Fatalf("feed with owner attribution = %+v, want Approval Owner", withOwner.Comments)
	}
}

func TestApprovalService_GetDecisionCommentsForItemIsBounded(t *testing.T) {
	env := newCommentFeedEnv(t)
	base := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	env.addApprovalFeedRow(t, "approval-0", base)

	var requestID int
	if err := env.db.QueryRow(`
		SELECT id FROM approval_requests WHERE item_id = ?
	`, env.itemID).Scan(&requestID); err != nil {
		t.Fatalf("find approval request: %v", err)
	}
	for i := 1; i < 6; i++ {
		if _, err := env.db.ExecWrite(`
			INSERT INTO approval_decisions
				(approval_request_id, actor_user_id, decision, comment, created_at)
			VALUES (?, ?, 'approve', ?, ?)
		`, requestID, env.userID, fmt.Sprintf("approval-%d", i), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("insert approval decision %d: %v", i, err)
		}
	}

	approval := services.NewApprovalService(env.db, nil, nil)
	comments, err := approval.GetDecisionCommentsForItem(env.itemID, false, services.CommentFeedOptions{Limit: 2})
	if err != nil {
		t.Fatalf("get bounded approval comments: %v", err)
	}
	if len(comments) != 3 {
		t.Fatalf("approval comments length = %d, want limit+1 (3)", len(comments))
	}
	if comments[0].Content != "approval-5" || comments[1].Content != "approval-4" || comments[2].Content != "approval-3" {
		t.Fatalf("approval comments = %+v, want newest three", comments)
	}
}

func TestCommentService_GetFeedByItemIDPaginatesMergedFeed(t *testing.T) {
	env := newCommentFeedEnv(t)
	service := services.NewCommentService(env.db)
	base := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	commentIDs := make([]int, 26)
	for i := range commentIDs {
		result, err := service.Create(services.CreateCommentParams{
			ItemID:      env.itemID,
			AuthorID:    env.userID,
			ActorUserID: env.userID,
			Content:     fmt.Sprintf("human-%02d", i),
		})
		if err != nil {
			t.Fatalf("create comment %d: %v", i, err)
		}
		commentIDs[i] = int(result.CommentID)
		createdAt := base.Add(time.Duration(i) * time.Minute)
		if _, err := env.db.ExecWrite(
			`UPDATE comments SET created_at = ?, updated_at = ? WHERE id = ?`,
			createdAt,
			createdAt,
			result.CommentID,
		); err != nil {
			t.Fatalf("set comment %d timestamp: %v", i, err)
		}
	}

	// The approval row shares the newest ordinary comment's timestamp. Its
	// negative synthetic feed ID must sort immediately after the positive ID.
	env.addApprovalFeedRow(t, "approval-feed-row", base.Add(25*time.Minute))

	page, err := service.GetFeedByItemID(env.itemID, false, services.CommentFeedOptions{})
	if err != nil {
		t.Fatalf("get first page: %v", err)
	}
	if len(page.Comments) != services.DefaultCommentFeedLimit {
		t.Fatalf("first page length = %d, want %d", len(page.Comments), services.DefaultCommentFeedLimit)
	}
	if !page.HasMore {
		t.Fatal("first page has_more = false, want true")
	}
	if page.Comments[0].Content != "human-25" || page.Comments[1].Content != "approval-feed-row" {
		t.Fatalf("merged ordering starts with %q, %q; want human-25 then approval row",
			page.Comments[0].Content, page.Comments[1].Content)
	}
	if page.Comments[1].Source != "approval" || page.Comments[1].ID >= 0 {
		t.Fatalf("approval projection = %+v, want negative approval row", page.Comments[1])
	}

	oldest := page.Comments[len(page.Comments)-1]
	older, err := service.GetFeedByItemID(env.itemID, false, services.CommentFeedOptions{
		Before: &services.CommentFeedCursor{CreatedAt: oldest.CreatedAt, ID: oldest.ID},
	})
	if err != nil {
		t.Fatalf("get older page: %v", err)
	}
	if older.HasMore {
		t.Fatal("older page has_more = true, want false")
	}
	if len(older.Comments) != 2 ||
		older.Comments[0].Content != "human-01" ||
		older.Comments[1].Content != "human-00" {
		t.Fatalf("older page = %+v, want human-01 then human-00", older.Comments)
	}

	newer, err := service.GetFeedByItemID(env.itemID, false, services.CommentFeedOptions{
		Since: &services.CommentFeedCursor{
			CreatedAt: base.Add(24 * time.Minute),
			ID:        commentIDs[24],
		},
	})
	if err != nil {
		t.Fatalf("get newer page: %v", err)
	}
	if newer.HasMore {
		t.Fatal("newer page has_more = true, want false")
	}
	if len(newer.Comments) != 2 ||
		newer.Comments[0].Content != "approval-feed-row" ||
		newer.Comments[1].Content != "human-25" {
		t.Fatalf("newer page = %+v, want approval row then human-25 in ascending cursor order", newer.Comments)
	}

	count, err := service.CountFeedByItemID(env.itemID)
	if err != nil {
		t.Fatalf("count feed: %v", err)
	}
	if count != 27 {
		t.Errorf("feed count = %d, want 27", count)
	}
}

func TestCommentService_GetFeedByItemIDSinceBurstDoesNotSkipRows(t *testing.T) {
	env := newCommentFeedEnv(t)
	service := services.NewCommentService(env.db)
	base := time.Date(2026, time.July, 27, 13, 0, 0, 0, time.UTC)

	cursor := services.CommentFeedCursor{CreatedAt: base, ID: 1}
	for i := 1; i <= 5; i++ {
		result, err := service.Create(services.CreateCommentParams{
			ItemID:      env.itemID,
			AuthorID:    env.userID,
			ActorUserID: env.userID,
			Content:     fmt.Sprintf("burst-%d", i),
		})
		if err != nil {
			t.Fatalf("create burst comment %d: %v", i, err)
		}
		createdAt := base.Add(time.Duration(i) * time.Minute)
		if _, err := env.db.ExecWrite(
			`UPDATE comments SET created_at = ?, updated_at = ? WHERE id = ?`,
			createdAt,
			createdAt,
			result.CommentID,
		); err != nil {
			t.Fatalf("set burst comment %d timestamp: %v", i, err)
		}
	}

	first, err := service.GetFeedByItemID(env.itemID, false, services.CommentFeedOptions{
		Limit: 2,
		Since: &cursor,
	})
	if err != nil {
		t.Fatalf("get first burst page: %v", err)
	}
	if !first.HasMore || len(first.Comments) != 2 ||
		first.Comments[0].Content != "burst-1" ||
		first.Comments[1].Content != "burst-2" {
		t.Fatalf("first burst page = %+v, has_more=%v", first.Comments, first.HasMore)
	}

	last := first.Comments[len(first.Comments)-1]
	second, err := service.GetFeedByItemID(env.itemID, false, services.CommentFeedOptions{
		Limit: 2,
		Since: &services.CommentFeedCursor{CreatedAt: last.CreatedAt, ID: last.ID},
	})
	if err != nil {
		t.Fatalf("get second burst page: %v", err)
	}
	if !second.HasMore || len(second.Comments) != 2 ||
		second.Comments[0].Content != "burst-3" ||
		second.Comments[1].Content != "burst-4" {
		t.Fatalf("second burst page = %+v, has_more=%v", second.Comments, second.HasMore)
	}
}
