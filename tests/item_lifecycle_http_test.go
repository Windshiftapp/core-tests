package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// lifecycleFixture creates a workspace with two tickets and a comment on the
// duplicate, all through the production HTTP API.
type lifecycleFixture struct {
	workspaceID int
	canonical   int
	duplicate   int
	commentID   int
}

func setupLifecycleFixture(t *testing.T, server *TestServer, name string) lifecycleFixture {
	t.Helper()

	emailFixtureSeq++
	key := fmt.Sprintf("LC%d", emailFixtureSeq)
	workspaceID, _ := CreateTestWorkspace(t, server, name+" WS", key)
	configSetID := GetDefaultConfigurationSet(t, server)
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	canonical := CreateTestItem(t, server, workspaceID, name+" canonical")
	duplicate := CreateTestItem(t, server, workspaceID, name+" duplicate")

	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", duplicate), map[string]interface{}{
		"content":    "duplicate detail worth keeping",
		"is_private": false,
	})
	AssertStatusCode(t, resp, http.StatusCreated)
	var comment struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, resp, &comment)
	resp.Body.Close()

	return lifecycleFixture{workspaceID: workspaceID, canonical: canonical, duplicate: duplicate, commentID: comment.ID}
}

func mergeItems(t *testing.T, server *TestServer, canonicalID int, sourceIDs []int) (int, *struct {
	Sources []struct {
		SourceItemID  int  `json:"source_item_id"`
		TargetItemID  int  `json:"target_item_id"`
		Merged        bool `json:"merged"`
		MovedComments int  `json:"moved_comments"`
	} `json:"sources"`
}) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/merge", canonicalID), map[string]interface{}{
		"source_item_ids": sourceIDs,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("merge: status=%d", resp.StatusCode)
	}
	var body struct {
		Sources []struct {
			SourceItemID  int  `json:"source_item_id"`
			TargetItemID  int  `json:"target_item_id"`
			Merged        bool `json:"merged"`
			MovedComments int  `json:"moved_comments"`
		} `json:"sources"`
	}
	DecodeJSON(t, resp, &body)
	return resp.StatusCode, &body
}

// TestItemLifecycleMergeHTTP verifies the merge boundary end to end: content
// moves, the duplicate carries a durable redirect, replays are idempotent,
// and conflicts are reported for re-targeting an already-merged duplicate.
func TestItemLifecycleMergeHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupLifecycleFixture(t, server, "Lifecycle merge")
	other := CreateTestItem(t, server, fix.workspaceID, "Other candidate")

	_, body := mergeItems(t, server, fix.canonical, []int{fix.duplicate})
	if len(body.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(body.Sources))
	}
	source := body.Sources[0]
	if source.SourceItemID != fix.duplicate || source.TargetItemID != fix.canonical || !source.Merged {
		t.Fatalf("merge result = %+v, want duplicate merged into canonical", source)
	}
	if source.MovedComments != 1 {
		t.Fatalf("moved_comments = %d, want 1", source.MovedComments)
	}

	// The comment followed the canonical.
	comments := GetItemComments(t, server, fix.canonical)
	if len(comments) != 1 {
		t.Fatalf("canonical comments = %d, want 1", len(comments))
	}

	// The duplicate exposes a durable redirect to the canonical.
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/merge-redirect", fix.duplicate), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var redirect struct {
		TargetItemID int `json:"target_item_id"`
	}
	DecodeJSON(t, resp, &redirect)
	if redirect.TargetItemID != fix.canonical {
		t.Fatalf("redirect target = %d, want %d", redirect.TargetItemID, fix.canonical)
	}

	// The canonical itself is not merged.
	unmerged := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/merge-redirect", fix.canonical), nil)
	defer unmerged.Body.Close()
	AssertStatusCode(t, unmerged, http.StatusNotFound)

	// Both sides of the fold land in item history with the other ticket's key
	// resolved, not a bare numeric id.
	canonicalEntry := findHistoryEntry(t, server, fix.canonical, "merged_item_id")
	if got := canonicalEntry["new_value"]; got != fmt.Sprintf("%d", fix.duplicate) {
		t.Fatalf("canonical merged_item_id new_value = %v, want %d", got, fix.duplicate)
	}
	if resolved, _ := canonicalEntry["resolved_new_value"].(string); resolved == "" {
		t.Fatalf("canonical merged_item_id has no resolved key: %v", canonicalEntry)
	}
	duplicateEntry := findHistoryEntry(t, server, fix.duplicate, "merged_into_item_id")
	if got := duplicateEntry["new_value"]; got != fmt.Sprintf("%d", fix.canonical) {
		t.Fatalf("duplicate merged_into_item_id new_value = %v, want %d", got, fix.canonical)
	}
	if resolved, _ := duplicateEntry["resolved_new_value"].(string); resolved == "" {
		t.Fatalf("duplicate merged_into_item_id has no resolved key: %v", duplicateEntry)
	}

	// Replaying the same merge is an idempotent no-op.
	_, replay := mergeItems(t, server, fix.canonical, []int{fix.duplicate})
	if len(replay.Sources) != 1 || replay.Sources[0].Merged {
		t.Fatalf("replay = %+v, want merged=false", replay.Sources)
	}

	// Re-targeting the already-merged duplicate conflicts.
	conflict := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/merge", other), map[string]interface{}{
		"source_item_ids": []int{fix.duplicate},
	})
	defer conflict.Body.Close()
	AssertStatusCode(t, conflict, http.StatusConflict)
}

// findHistoryEntry returns the first history row for an item with the given
// field name, failing the test when none is present.
func findHistoryEntry(t *testing.T, server *TestServer, itemID int, field string) map[string]interface{} {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%d/history", itemID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var history []map[string]interface{}
	DecodeJSON(t, resp, &history)
	for _, entry := range history {
		if name, _ := entry["field_name"].(string); name == field {
			return entry
		}
	}
	t.Fatalf("item %d history has no %q entry: %v", itemID, field, history)
	return nil
}

// TestItemLifecycleMergedDuplicateIsReadOnlyRedirect verifies the WI-1528
// contract: after a merge the duplicate disappears from item lists, and both
// comment creation and item updates are refused so its thread cannot silently
// diverge from the canonical.
func TestItemLifecycleMergedDuplicateIsReadOnlyRedirect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupLifecycleFixture(t, server, "Lifecycle readonly")
	if _, body := mergeItems(t, server, fix.canonical, []int{fix.duplicate}); len(body.Sources) != 1 || !body.Sources[0].Merged {
		t.Fatalf("merge did not fold the duplicate: %+v", body.Sources)
	}

	t.Run("duplicate is excluded from the workspace item list", func(t *testing.T) {
		items := GetItemsByWorkspace(t, server, fix.workspaceID)
		for _, item := range items {
			if id, _ := item["id"].(float64); int(id) == fix.duplicate {
				t.Fatalf("merged duplicate %d still appears in the item list", fix.duplicate)
			}
		}
		foundCanonical := false
		for _, item := range items {
			if id, _ := item["id"].(float64); int(id) == fix.canonical {
				foundCanonical = true
			}
		}
		if !foundCanonical {
			t.Fatal("canonical ticket is missing from the item list")
		}
	})

	t.Run("commenting on the duplicate is refused", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", fix.duplicate), map[string]interface{}{
			"content": "should not land on the duplicate",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)

		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		DecodeJSON(t, resp, &body)
		if !strings.Contains(body.Error.Message, fmt.Sprintf("merged into item %d", fix.canonical)) {
			t.Fatalf("conflict message = %q, want a pointer to the canonical", body.Error.Message)
		}
		if got := len(GetItemComments(t, server, fix.canonical)); got != 1 {
			t.Fatalf("canonical comments = %d, want the original 1", got)
		}
	})

	t.Run("editing the duplicate is refused", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/items/%d", fix.duplicate), map[string]interface{}{
			"title": "edited after merge",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)
	})
}

// TestItemLifecycleMergeHTTPRequiresWorkspaceAccess verifies the denial
// contract: a user without item access gets existence-masking 404s and no
// state mutation.
func TestItemLifecycleMergeHTTPRequiresWorkspaceAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupLifecycleFixture(t, server, "Lifecycle denial")

	otherUserID, otherUsername, otherPassword := CreateTestUserWithCredentials(
		t, server, "lifecycle_other", "lifecycle-other@test.com",
	)
	// Workspaces are open-editor by default. An explicit Viewer assignment
	// closes the editor role for this user, so the merge endpoint must refuse
	// them while reads keep working.
	AssignWorkspaceRole(t, server, otherUserID, fix.workspaceID, "Viewer")
	otherToken, _ := CreateAuthCredentialsForUser(t, server, otherUsername, otherPassword)

	resp := MakeAuthRequestWithToken(t, server, otherToken, http.MethodPost, fmt.Sprintf("/items/%d/merge", fix.canonical), map[string]interface{}{
		"source_item_ids": []int{fix.duplicate},
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusNotFound)

	redirect := MakeAuthRequestWithToken(t, server, otherToken, http.MethodGet, fmt.Sprintf("/items/%d/merge-redirect", fix.duplicate), nil)
	defer redirect.Body.Close()
	AssertStatusCode(t, redirect, http.StatusNotFound)

	// Nothing moved.
	comments := GetItemComments(t, server, fix.canonical)
	if len(comments) != 0 {
		t.Fatalf("canonical comments after denial = %d, want 0", len(comments))
	}
}

// TestItemLifecycleSplitHTTP verifies the split boundary: a child ticket with
// explicit ownership receives exactly the selected comments.
func TestItemLifecycleSplitHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	emailFixtureSeq++
	key := fmt.Sprintf("LC%d", emailFixtureSeq)
	workspaceID, _ := CreateTestWorkspace(t, server, "Split WS", key)
	configSetID := GetDefaultConfigurationSet(t, server)
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)
	source := CreateTestItem(t, server, workspaceID, "Complex request")

	createComment := func(content string) int {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", source), map[string]interface{}{
			"content":    content,
			"is_private": false,
		})
		AssertStatusCode(t, resp, http.StatusCreated)
		var comment struct {
			ID int `json:"id"`
		}
		DecodeJSON(t, resp, &comment)
		resp.Body.Close()
		return comment.ID
	}
	keep := createComment("stays behind")
	move1 := createComment("moves out")
	move2 := createComment("also moves")

	var assigneeID int
	if err := server.DB().QueryRow(`SELECT id FROM users WHERE username = 'admin' LIMIT 1`).Scan(&assigneeID); err != nil {
		t.Fatalf("load assignee: %v", err)
	}

	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/split", source), map[string]interface{}{
		"title":       "Follow-up request",
		"description": "Carved out of the original",
		"comment_ids": []int{move1, move2},
		"assignee_id": assigneeID,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var body struct {
		SplitItemID   int `json:"split_item_id"`
		MovedComments int `json:"moved_comments"`
	}
	DecodeJSON(t, resp, &body)
	if body.SplitItemID == 0 || body.SplitItemID == source {
		t.Fatalf("split_item_id = %d, want a new item", body.SplitItemID)
	}
	if body.MovedComments != 2 {
		t.Fatalf("moved_comments = %d, want 2", body.MovedComments)
	}

	// The subticket is a real child with the moved content and explicit owner.
	child := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", body.SplitItemID), nil)
	defer child.Body.Close()
	AssertStatusCode(t, child, http.StatusOK)
	var childItem struct {
		Title      string `json:"title"`
		ParentID   *int   `json:"parent_id"`
		ItemTypeID *int   `json:"item_type_id"`
	}
	DecodeJSON(t, child, &childItem)
	if childItem.Title != "Follow-up request" {
		t.Fatalf("child title = %q, want 'Follow-up request'", childItem.Title)
	}
	if childItem.ParentID == nil || *childItem.ParentID != source {
		t.Fatalf("child parent = %v, want %d", childItem.ParentID, source)
	}
	// The source is a Task (lowest regular level), so the subticket must be the
	// generic Sub-task type rather than another Task.
	itemTypes := GetItemTypes(t, server, configSetID)
	if childItem.ItemTypeID == nil || *childItem.ItemTypeID != itemTypes["Sub-task"] {
		t.Fatalf("child item type = %v, want generic Sub-task %d", childItem.ItemTypeID, itemTypes["Sub-task"])
	}

	childComments := GetItemComments(t, server, body.SplitItemID)
	if len(childComments) != 2 {
		t.Fatalf("child comments = %d, want 2", len(childComments))
	}
	sourceComments := GetItemComments(t, server, source)
	if len(sourceComments) != 1 {
		t.Fatalf("source comments after split = %d, want 1", len(sourceComments))
	}
	_ = keep

	// Splitting with a comment from another item is a validation failure.
	foreign := CreateTestItem(t, server, workspaceID, "Other request")
	foreignComment := createCommentOn(t, server, foreign, "not yours")
	bad := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/split", source), map[string]interface{}{
		"title":       "Bad split",
		"comment_ids": []int{foreignComment},
	})
	defer bad.Body.Close()
	AssertStatusCode(t, bad, http.StatusBadRequest)
}

// TestItemLifecycleSplitHTTPHonorsExplicitChildType verifies a caller can pick
// a legal sibling child type (Bug next to Task) through the API, and that an
// illegal placement (the source's own type) is rejected.
func TestItemLifecycleSplitHTTPHonorsExplicitChildType(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	emailFixtureSeq++
	key := fmt.Sprintf("LC%d", emailFixtureSeq)
	workspaceID, _ := CreateTestWorkspace(t, server, "Split type WS", key)
	configSetID := GetDefaultConfigurationSet(t, server)
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	itemTypes := GetItemTypes(t, server, configSetID)
	source := CreateTestItemWithType(t, server, workspaceID, "Story request", itemTypes["Story"])
	commentID := createCommentOn(t, server, source, "moves to the bug")

	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/split", source), map[string]interface{}{
		"title":        "Bug follow-up",
		"item_type_id": itemTypes["Bug"],
		"comment_ids":  []int{commentID},
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var body struct {
		SplitItemID int `json:"split_item_id"`
	}
	DecodeJSON(t, resp, &body)

	child := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", body.SplitItemID), nil)
	defer child.Body.Close()
	AssertStatusCode(t, child, http.StatusOK)
	var childItem struct {
		ItemTypeID *int `json:"item_type_id"`
	}
	DecodeJSON(t, child, &childItem)
	if childItem.ItemTypeID == nil || *childItem.ItemTypeID != itemTypes["Bug"] {
		t.Fatalf("child item type = %v, want requested Bug %d", childItem.ItemTypeID, itemTypes["Bug"])
	}

	// The source's own type is not one level below itself, so it is refused.
	secondComment := createCommentOn(t, server, source, "invalid placement")
	bad := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/split", source), map[string]interface{}{
		"title":        "Same type",
		"item_type_id": itemTypes["Story"],
		"comment_ids":  []int{secondComment},
	})
	defer bad.Body.Close()
	AssertStatusCode(t, bad, http.StatusBadRequest)
}

func createCommentOn(t *testing.T, server *TestServer, itemID int, content string) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", itemID), map[string]interface{}{
		"content":    content,
		"is_private": false,
	})
	AssertStatusCode(t, resp, http.StatusCreated)
	var comment struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, resp, &comment)
	resp.Body.Close()
	return comment.ID
}

// TestItemLifecycleMergeHTTPRejectsMergingIntoOwnSubticket verifies the
// descendant cycle guard: the canonical may never live under a merged-away
// duplicate, because deleting the duplicate would cascade-delete the
// canonical and everything merged into it. The guard must follow the live
// parent_id chain through every depth, and legitimate merges must still pass.
func TestItemLifecycleMergeHTTPRejectsMergingIntoOwnSubticket(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	emailFixtureSeq++
	key := fmt.Sprintf("LC%d", emailFixtureSeq)
	workspaceID, _ := CreateTestWorkspace(t, server, "Merge cycle WS", key)
	configSetID := GetDefaultConfigurationSet(t, server)
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	// Build parent -> child -> grandchild through the production split API
	// (each split carries one comment, the minimum it accepts). Start at Story
	// so the chain has two levels below it: Story -> Task -> Sub-task.
	itemTypes := GetItemTypes(t, server, configSetID)
	parent := CreateTestItemWithType(t, server, workspaceID, "Merge cycle parent", itemTypes["Story"])
	splitOut := func(sourceID int, title string) int {
		commentID := createCommentOn(t, server, sourceID, "content to carry over")
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/split", sourceID), map[string]interface{}{
			"title":       title,
			"description": "Subticket for the merge cycle test",
			"comment_ids": []int{commentID},
		})
		AssertStatusCode(t, resp, http.StatusCreated)
		var body struct {
			SplitItemID int `json:"split_item_id"`
		}
		DecodeJSON(t, resp, &body)
		resp.Body.Close()
		if body.SplitItemID == 0 {
			t.Fatalf("split of item %d produced no subticket", sourceID)
		}
		return body.SplitItemID
	}
	child := splitOut(parent, "Merge cycle child")
	grandchild := splitOut(child, "Merge cycle grandchild")

	// Folding the parent into its own child must conflict: the child would
	// become canonical while living under the merged-away parent.
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/merge", child), map[string]interface{}{
		"source_item_ids": []int{parent},
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusConflict)

	// The same guard must hold through two hierarchy levels.
	deep := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/merge", grandchild), map[string]interface{}{
		"source_item_ids": []int{parent},
	})
	defer deep.Body.Close()
	AssertStatusCode(t, deep, http.StatusConflict)

	// An item is never its own duplicate.
	self := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/merge", parent), map[string]interface{}{
		"source_item_ids": []int{parent},
	})
	defer self.Body.Close()
	AssertStatusCode(t, self, http.StatusBadRequest)

	// None of the denials left a merge redirect behind.
	for _, itemID := range []int{parent, child, grandchild} {
		redirect := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/merge-redirect", itemID), nil)
		redirect.Body.Close()
		AssertStatusCode(t, redirect, http.StatusNotFound)
	}

	// The guard must not overfire: an unrelated duplicate still merges.
	unrelated := CreateTestItem(t, server, workspaceID, "Unrelated duplicate")
	_, body := mergeItems(t, server, parent, []int{unrelated})
	if len(body.Sources) != 1 || !body.Sources[0].Merged {
		t.Fatalf("unrelated merge = %+v, want merged=true", body.Sources)
	}
	redirect := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/merge-redirect", unrelated), nil)
	defer redirect.Body.Close()
	AssertStatusCode(t, redirect, http.StatusOK)
	var target struct {
		TargetItemID int `json:"target_item_id"`
	}
	DecodeJSON(t, redirect, &target)
	if target.TargetItemID != parent {
		t.Fatalf("unrelated merge redirect target = %d, want %d", target.TargetItemID, parent)
	}
}
