// mcp_comments_test covers list_comments and add_comment.
package tests

import (
	"strings"
	"testing"
)

func TestMCP_Comments_RoundTrip(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[0]

	// 1. list_comments on a fresh item is empty.
	var listed struct {
		Comments []struct {
			ID      int    `json:"id"`
			Content string `json:"content"`
		} `json:"comments"`
	}
	callTool(t, session, "list_comments", map[string]interface{}{"item_id": target.ID}, &listed)
	if len(listed.Comments) != 0 {
		t.Fatalf("expected 0 comments on fresh seeded item, got %d", len(listed.Comments))
	}

	// 2. Add three comments.
	for _, content := range []string{"first", "second", "third"} {
		callTool(t, session, "add_comment", map[string]interface{}{
			"item_id": target.ID,
			"content": content,
		}, nil)
	}

	// 3. List again — must return all three, including their content.
	listed.Comments = nil
	callTool(t, session, "list_comments", map[string]interface{}{"item_id": target.ID}, &listed)
	if len(listed.Comments) != 3 {
		t.Fatalf("expected 3 comments, got %d (%+v)", len(listed.Comments), listed.Comments)
	}
	for _, want := range []string{"first", "second", "third"} {
		found := false
		for _, c := range listed.Comments {
			if strings.Contains(c.Content, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("comment with content %q missing (got %+v)", want, listed.Comments)
		}
	}
}
