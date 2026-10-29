//go:build test

package services_test

import (
	"testing"
	"time"

	"windshift/internal/services"
	"windshift/internal/testutils/factory"
)

func TestCommentService_GetFeedByItemIDsReturnsNewestPagePerItem(t *testing.T) {
	env := newCommentFeedEnv(t)
	service := services.NewCommentService(env.db)
	factoryDB := factory.NewTestFactory(env.db)
	secondItem, err := factoryDB.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceIDForBatchTest(t, env),
		Title:       "Batch second item",
	})
	if err != nil {
		t.Fatalf("insert second item: %v", err)
	}
	thirdItem, err := factoryDB.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceIDForBatchTest(t, env),
		Title:       "Batch third item",
	})
	if err != nil {
		t.Fatalf("insert third item: %v", err)
	}

	base := time.Date(2026, time.September, 11, 10, 0, 0, 0, time.UTC)
	addBatchComment(t, env, service, env.itemID, "a-1", base)
	addBatchComment(t, env, service, env.itemID, "a-2", base.Add(time.Minute))
	addBatchComment(t, env, service, env.itemID, "a-3", base.Add(2*time.Minute))
	addBatchComment(t, env, service, secondItem, "b-1", base.Add(3*time.Minute))

	pages, err := service.GetFeedByItemIDs(
		[]int{secondItem, env.itemID, thirdItem, env.itemID},
		false,
		services.CommentFeedOptions{Limit: 2},
	)
	if err != nil {
		t.Fatalf("get batch feeds: %v", err)
	}

	if len(pages) != 3 {
		t.Fatalf("pages = %d entries, want 3 (duplicates collapsed)", len(pages))
	}
	firstPage := pages[env.itemID]
	if !firstPage.HasMore {
		t.Error("first item has_more = false, want true (3 comments, limit 2)")
	}
	if len(firstPage.Comments) != 2 ||
		firstPage.Comments[0].Content != "a-3" ||
		firstPage.Comments[1].Content != "a-2" {
		t.Errorf("first item page = %+v, want [a-3 a-2] newest first", firstPage.Comments)
	}
	secondPage := pages[secondItem]
	if secondPage.HasMore {
		t.Error("second item has_more = true, want false")
	}
	if len(secondPage.Comments) != 1 || secondPage.Comments[0].Content != "b-1" {
		t.Errorf("second item page = %+v, want [b-1]", secondPage.Comments)
	}
	thirdPage := pages[thirdItem]
	if thirdPage.HasMore || len(thirdPage.Comments) != 0 {
		t.Errorf("third item page = %+v has_more=%v, want empty page", thirdPage.Comments, thirdPage.HasMore)
	}
}

func TestCommentService_GetFeedByItemIDsMergesApprovalCommentsWithTieBreak(t *testing.T) {
	env := newCommentFeedEnv(t)
	service := services.NewCommentService(env.db)
	base := time.Date(2026, time.September, 11, 11, 0, 0, 0, time.UTC)
	addBatchComment(t, env, service, env.itemID, "human-0", base)
	addBatchComment(t, env, service, env.itemID, "human-1", base.Add(time.Minute))
	addBatchComment(t, env, service, env.itemID, "human-2", base.Add(2*time.Minute))
	// Same timestamp as human-2: the positive human ID must sort before the
	// synthetic negative approval ID in newest-first order.
	env.addApprovalFeedRow(t, "approval-row", base.Add(2*time.Minute))

	pages, err := service.GetFeedByItemIDs([]int{env.itemID}, false, services.CommentFeedOptions{Limit: 2})
	if err != nil {
		t.Fatalf("get batch feed: %v", err)
	}
	page := pages[env.itemID]
	if !page.HasMore {
		t.Fatal("has_more = false, want true (4 merged comments, limit 2)")
	}
	if len(page.Comments) != 2 {
		t.Fatalf("page length = %d, want 2", len(page.Comments))
	}
	if page.Comments[0].Content != "human-2" {
		t.Errorf("first comment = %q, want human-2 (positive ID wins timestamp tie)", page.Comments[0].Content)
	}
	if page.Comments[1].Content != "approval-row" || page.Comments[1].Source != "approval" || page.Comments[1].ID >= 0 {
		t.Errorf("second comment = %+v, want synthetic approval row", page.Comments[1])
	}
}

func TestCommentService_GetFeedByItemIDsEmptyInput(t *testing.T) {
	env := newCommentFeedEnv(t)
	service := services.NewCommentService(env.db)

	pages, err := service.GetFeedByItemIDs(nil, false, services.CommentFeedOptions{})
	if err != nil {
		t.Fatalf("get batch feeds for nil input: %v", err)
	}
	if len(pages) != 0 {
		t.Fatalf("pages = %d entries, want 0", len(pages))
	}
}

func addBatchComment(t *testing.T, env commentFeedEnv, service *services.CommentService, itemID int, content string, createdAt time.Time) {
	t.Helper()
	result, err := service.Create(services.CreateCommentParams{
		ItemID:      itemID,
		AuthorID:    env.userID,
		ActorUserID: env.userID,
		Content:     content,
	})
	if err != nil {
		t.Fatalf("create comment %q: %v", content, err)
	}
	if _, err := env.db.ExecWrite(
		`UPDATE comments SET created_at = ?, updated_at = ? WHERE id = ?`,
		createdAt,
		createdAt,
		result.CommentID,
	); err != nil {
		t.Fatalf("set comment %q timestamp: %v", content, err)
	}
}

func workspaceIDForBatchTest(t *testing.T, env commentFeedEnv) int {
	t.Helper()
	var workspaceID int
	if err := env.db.QueryRow(`SELECT workspace_id FROM items WHERE id = ?`, env.itemID).Scan(&workspaceID); err != nil {
		t.Fatalf("find workspace for item: %v", err)
	}
	return workspaceID
}
