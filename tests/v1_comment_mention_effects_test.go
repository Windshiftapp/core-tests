package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// v1CommentMentionEnv creates a workspace, an item, a mentionable viewer, and
// an editor token for exercising mention side effects on the v1 comment API.
func v1CommentMentionEnv(t *testing.T, suffix string) (server *TestServer, itemID, targetID int, targetUsername, token string) {
	t.Helper()

	server, _ = StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Mention effects "+suffix, shortKey("MEF"+suffix))
	LockDownWorkspace(t, server, workspaceID)
	itemID = CreateTestItem(t, server, workspaceID, "Mention effects target "+suffix)

	targetID, targetUsername, _ = CreateTestUserWithCredentials(t, server,
		"mention_target_"+suffix, "mention_target_"+suffix+"@test.com")
	AssignWorkspaceRole(t, server, targetID, workspaceID, "Viewer")

	authorID, authorUsername, authorPassword := CreateTestUserWithCredentials(t, server,
		"mention_author_"+suffix, "mention_author_"+suffix+"@test.com")
	AssignWorkspaceRole(t, server, authorID, workspaceID, "Editor")
	token = createTokenWithScopesAsUser(t, server, authorUsername, authorPassword,
		[]string{"items:read", "items:write", "items:delete"})

	return server, itemID, targetID, targetUsername, token
}

// createMentionComment posts a comment mentioning targetUsername through the v1
// API and returns its comment ID.
func createMentionComment(t *testing.T, server *TestServer, itemID int, targetUsername, token, content string) int {
	t.Helper()
	create := MakeBearerRequestWithToken(t, server, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/items/%d/comments", itemID), map[string]any{"content": content})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var comment map[string]any
	DecodeJSON(t, create, &comment)
	return ExtractIDFromResponse(t, comment)
}

func countMentions(t *testing.T, server *TestServer, commentID int) int {
	t.Helper()
	var count int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM mentions WHERE source_type = 'comment' AND source_id = ?`, commentID).Scan(&count); err != nil {
		t.Fatalf("count mentions: %v", err)
	}
	return count
}

func TestV1CommentDeleteRemovesMentionAndPendingEmail(t *testing.T) {
	server, itemID, _, targetUsername, token := v1CommentMentionEnv(t, "delete")
	commentID := createMentionComment(t, server, itemID, targetUsername, token,
		"ping @"+targetUsername+" please review")

	if got := countMentions(t, server, commentID); got != 1 {
		t.Fatalf("mentions after create = %d, want 1", got)
	}
	var outboxRows int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM mention_email_outbox`).Scan(&outboxRows); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if outboxRows != 1 {
		t.Fatalf("outbox rows after create = %d, want 1", outboxRows)
	}

	del := MakeBearerRequestWithToken(t, server, token, http.MethodDelete,
		fmt.Sprintf("/rest/api/v1/comments/%d", commentID), nil)
	defer del.Body.Close()
	AssertStatusCode(t, del, http.StatusNoContent)

	if got := countMentions(t, server, commentID); got != 0 {
		t.Fatalf("mentions after v1 delete = %d, want 0", got)
	}
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM mention_email_outbox`).Scan(&outboxRows); err != nil {
		t.Fatalf("count outbox rows after delete: %v", err)
	}
	if outboxRows != 0 {
		t.Fatalf("outbox rows after v1 delete = %d, want 0", outboxRows)
	}
}

func TestV1CommentEditRemovesMention(t *testing.T) {
	server, itemID, _, targetUsername, token := v1CommentMentionEnv(t, "editremove")
	commentID := createMentionComment(t, server, itemID, targetUsername, token,
		"ping @"+targetUsername+" please review")

	if got := countMentions(t, server, commentID); got != 1 {
		t.Fatalf("mentions after create = %d, want 1", got)
	}

	edit := MakeBearerRequestWithToken(t, server, token, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/comments/%d", commentID), map[string]any{"content": "no mention now"})
	defer edit.Body.Close()
	AssertStatusCode(t, edit, http.StatusOK)

	if got := countMentions(t, server, commentID); got != 0 {
		t.Fatalf("mentions after v1 edit = %d, want 0", got)
	}
}

func TestV1CommentEditRefreshesPendingMentionEmail(t *testing.T) {
	server, itemID, _, targetUsername, token := v1CommentMentionEnv(t, "editcontent")
	commentID := createMentionComment(t, server, itemID, targetUsername, token,
		"original body for @"+targetUsername+" here")

	var content string
	if err := server.DB().QueryRow(
		`SELECT content FROM mention_email_outbox ORDER BY id DESC LIMIT 1`).Scan(&content); err != nil {
		t.Fatalf("read outbox content after create: %v", err)
	}
	if content != "original body for @"+targetUsername+" here" {
		t.Fatalf("outbox content after create = %q", content)
	}

	edit := MakeBearerRequestWithToken(t, server, token, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/comments/%d", commentID),
		map[string]any{"content": "edited body for @" + targetUsername + " here"})
	defer edit.Body.Close()
	AssertStatusCode(t, edit, http.StatusOK)

	if err := server.DB().QueryRow(
		`SELECT content FROM mention_email_outbox ORDER BY id DESC LIMIT 1`).Scan(&content); err != nil {
		t.Fatalf("read outbox content after edit: %v", err)
	}
	if content != "edited body for @"+targetUsername+" here" {
		t.Fatalf("outbox content after v1 edit = %q, want the edited body", content)
	}
}
