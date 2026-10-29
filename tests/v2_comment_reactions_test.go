package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type commentReactionGroup struct {
	Reaction  string   `json:"reaction"`
	Count     int      `json:"count"`
	UserIDs   []int    `json:"user_ids"`
	UserNames []string `json:"user_names"`
}

type v2Comment struct {
	ID        int                    `json:"id"`
	Content   string                 `json:"content"`
	Reactions []commentReactionGroup `json:"reactions"`
}

func createV2ReactionComment(t *testing.T, server *TestServer, itemID int, content string) int {
	t.Helper()
	response := MakeBearerRequest(t, server, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/items/%d/comments", itemID), map[string]any{"content": content})
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusCreated)
	var document struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, response, &document)
	if document.Data.ID == 0 {
		t.Fatal("created comment has no id")
	}
	return document.Data.ID
}

func postCommentReaction(t *testing.T, server *TestServer, token string, commentID int, reaction string) (int, []commentReactionGroup) {
	t.Helper()
	response := MakeBearerRequestWithToken(t, server, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/comments/%d/reactions", commentID), map[string]any{"reaction": reaction})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil
	}
	var document struct {
		Data []commentReactionGroup `json:"data"`
	}
	DecodeJSON(t, response, &document)
	return response.StatusCode, document.Data
}

func deleteCommentReaction(t *testing.T, server *TestServer, token string, commentID int, reaction string) int {
	t.Helper()
	response := MakeBearerRequestWithToken(t, server, token, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/comments/%d/reactions/%s", commentID, reaction), nil)
	defer response.Body.Close()
	return response.StatusCode
}

func getV2Comment(t *testing.T, server *TestServer, token string, commentID int, includeReactions bool) (int, v2Comment) {
	t.Helper()
	path := fmt.Sprintf("/rest/api/v2/comments/%d", commentID)
	if includeReactions {
		path += "?include_reactions=true"
	}
	response := MakeBearerRequestWithToken(t, server, token, http.MethodGet, path, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, v2Comment{}
	}
	var document struct {
		Data v2Comment `json:"data"`
	}
	DecodeJSON(t, response, &document)
	return response.StatusCode, document.Data
}

// listV2Comments returns the comment feed for one item, optionally hydrated
// with grouped reactions.
func listV2Comments(t *testing.T, server *TestServer, token string, itemID int, includeReactions bool) (int, []v2Comment) {
	t.Helper()
	path := fmt.Sprintf("/rest/api/v2/items/%d/comments", itemID)
	if includeReactions {
		path += "?include_reactions=true"
	}
	response := MakeBearerRequestWithToken(t, server, token, http.MethodGet, path, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil
	}
	var document struct {
		Data struct {
			Comments []v2Comment `json:"comments"`
		} `json:"data"`
	}
	DecodeJSON(t, response, &document)
	return response.StatusCode, document.Data.Comments
}

// batchCommentReactions returns the reactions the batch endpoint reports for
// the comments of each requested item.
func batchCommentReactions(t *testing.T, server *TestServer, token string, itemIDs []int, includeReactions bool) map[int][]commentReactionGroup {
	t.Helper()
	body := map[string]any{"item_ids": itemIDs}
	if includeReactions {
		body["include_reactions"] = true
	}
	response := MakeBearerRequestWithToken(t, server, token, http.MethodPost,
		"/rest/api/v2/comments/batch", body)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var document struct {
		Data []struct {
			ItemID   int `json:"item_id"`
			Comments []struct {
				ID        int                    `json:"id"`
				Reactions []commentReactionGroup `json:"reactions"`
			} `json:"comments"`
		} `json:"data"`
	}
	DecodeJSON(t, response, &document)
	byItem := make(map[int][]commentReactionGroup, len(document.Data))
	for _, entry := range document.Data {
		for _, comment := range entry.Comments {
			byItem[entry.ItemID] = comment.Reactions
		}
	}
	return byItem
}

func TestV2CommentReactionsRoundTripAndIdempotency(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Comment reactions", shortKey("CRX"))
	itemID := CreateTestItem(t, server, workspaceID, "Reaction item")
	commentID := createV2ReactionComment(t, server, itemID, "please review")

	status, groups := postCommentReaction(t, server, server.BearerToken, commentID, "thumbs_up")
	if status != http.StatusOK {
		t.Fatalf("add reaction status = %d, want 200", status)
	}
	if len(groups) != 1 || groups[0].Reaction != "thumbs_up" || groups[0].Count != 1 {
		t.Fatalf("reactions after add = %+v, want one thumbs_up with count 1", groups)
	}

	// Adding the same reaction twice must not create a second row.
	status, groups = postCommentReaction(t, server, server.BearerToken, commentID, "thumbs_up")
	if status != http.StatusOK {
		t.Fatalf("re-add reaction status = %d, want 200", status)
	}
	if len(groups) != 1 || groups[0].Count != 1 {
		t.Fatalf("reactions after duplicate add = %+v, want a single thumbs_up with count 1", groups)
	}

	status, comment := getV2Comment(t, server, server.BearerToken, commentID, true)
	if status != http.StatusOK {
		t.Fatalf("get comment status = %d, want 200", status)
	}
	if len(comment.Reactions) != 1 || comment.Reactions[0].Reaction != "thumbs_up" {
		t.Fatalf("get comment reactions = %+v, want one thumbs_up", comment.Reactions)
	}

	if batched := batchCommentReactions(t, server, server.BearerToken, []int{itemID}, true); len(batched[itemID]) != 1 || batched[itemID][0].Reaction != "thumbs_up" {
		t.Fatalf("batch reactions = %+v, want one thumbs_up for item %d", batched[itemID], itemID)
	}

	if status := deleteCommentReaction(t, server, server.BearerToken, commentID, "thumbs_up"); status != http.StatusNoContent {
		t.Fatalf("remove reaction status = %d, want 204", status)
	}
	// Removing a reaction that no longer exists is a no-op.
	if status := deleteCommentReaction(t, server, server.BearerToken, commentID, "thumbs_up"); status != http.StatusNoContent {
		t.Fatalf("second remove reaction status = %d, want 204", status)
	}

	status, comment = getV2Comment(t, server, server.BearerToken, commentID, true)
	if status != http.StatusOK {
		t.Fatalf("get comment status = %d, want 200", status)
	}
	if len(comment.Reactions) != 0 {
		t.Fatalf("reactions after remove = %+v, want none", comment.Reactions)
	}
}

func TestV2CommentReactionsAreOptIn(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Comment reactions opt-in", shortKey("CRO"))
	itemID := CreateTestItem(t, server, workspaceID, "Reaction item")
	commentID := createV2ReactionComment(t, server, itemID, "please review")
	if status, _ := postCommentReaction(t, server, server.BearerToken, commentID, "check"); status != http.StatusOK {
		t.Fatalf("add reaction status = %d, want 200", status)
	}

	if status, comment := getV2Comment(t, server, server.BearerToken, commentID, false); status != http.StatusOK || len(comment.Reactions) != 0 {
		t.Fatalf("default get = %d %+v, want 200 with no reactions", status, comment.Reactions)
	}
	if status, comments := listV2Comments(t, server, server.BearerToken, itemID, false); status != http.StatusOK || len(comments) != 1 || len(comments[0].Reactions) != 0 {
		t.Fatalf("default list = %d %+v, want one comment without reactions", status, comments)
	}
	if batched := batchCommentReactions(t, server, server.BearerToken, []int{itemID}, false); len(batched[itemID]) != 0 {
		t.Fatalf("default batch reactions = %+v, want none", batched[itemID])
	}

	if _, comment := getV2Comment(t, server, server.BearerToken, commentID, true); len(comment.Reactions) != 1 {
		t.Fatalf("opt-in get reactions = %+v, want one", comment.Reactions)
	}
	if _, comments := listV2Comments(t, server, server.BearerToken, itemID, true); len(comments) != 1 || len(comments[0].Reactions) != 1 {
		t.Fatalf("opt-in list = %+v, want one comment with one reaction", comments)
	}
	if batched := batchCommentReactions(t, server, server.BearerToken, []int{itemID}, true); len(batched[itemID]) != 1 {
		t.Fatalf("opt-in batch reactions = %+v, want one", batched[itemID])
	}
}

func TestV2CommentReactionsRejectUnknownKeysAndMissingComments(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Comment reactions invalid", shortKey("CRI"))
	itemID := CreateTestItem(t, server, workspaceID, "Reaction item")
	commentID := createV2ReactionComment(t, server, itemID, "please review")

	status, _ := postCommentReaction(t, server, server.BearerToken, commentID, "rocket")
	if status != http.StatusBadRequest {
		t.Fatalf("unknown reaction status = %d, want 400", status)
	}
	if status := deleteCommentReaction(t, server, server.BearerToken, commentID, "rocket"); status != http.StatusBadRequest {
		t.Fatalf("unknown reaction delete status = %d, want 400", status)
	}
	if status, _ := postCommentReaction(t, server, server.BearerToken, commentID+100000, "check"); status != http.StatusNotFound {
		t.Fatalf("missing comment status = %d, want 404", status)
	}
}

func TestV2CommentReactionsRequireWorkspaceAccess(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Comment reactions access", shortKey("CRA"))
	itemID := CreateTestItem(t, server, workspaceID, "Reaction item")
	commentID := createV2ReactionComment(t, server, itemID, "please review")
	// Gating the workspace hides it from users without a role, so the outsider
	// sees the same 404 as a caller without item.comment.
	LockDownWorkspace(t, server, workspaceID)

	_, username, password := CreateTestUserWithCredentials(t, server, "reaction_outsider", "reaction_outsider@test.com")
	token := createTokenWithScopesAsUser(t, server, username, password, []string{"items:read", "items:write"})

	// Without item.view the comment itself is invisible, so reacting is 404.
	if status, _ := postCommentReaction(t, server, token, commentID, "check"); status != http.StatusNotFound {
		t.Fatalf("outsider add reaction status = %d, want 404", status)
	}
	if status, _ := getV2Comment(t, server, token, commentID, true); status != http.StatusNotFound {
		t.Fatalf("outsider get comment status = %d, want 404", status)
	}
	if status := deleteCommentReaction(t, server, token, commentID, "check"); status != http.StatusNotFound {
		t.Fatalf("outsider remove reaction status = %d, want 404", status)
	}
}
