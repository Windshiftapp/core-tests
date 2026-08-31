package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestItemUpdate_CookieAndRESTV1Contract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Item Update Contract", shortKey("IUC"))

	mentionedUserID, _, _ := CreateTestUserWithCredentials(t, server,
		"update_mention_target", "update_mention_target@test.com")
	AssignWorkspaceRole(t, server, mentionedUserID, workspaceID, "Viewer")

	for _, surface := range []string{"cookie", "v1"} {
		t.Run(surface, func(t *testing.T) {
			itemID := CreateTestItem(t, server, workspaceID, "Original title")
			wantTitle := "Promise<Anything> updated title"
			wantDescription := "hello @update_mention_target<script>bad()</script><br/>done <https://example.com/docs?q=windshift&view=full>"
			body := map[string]interface{}{
				"title":       "\n" + wantTitle + "  ",
				"description": wantDescription,
			}

			var response *http.Response
			if surface == "cookie" {
				response = MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/items/%d", itemID), body)
			} else {
				response = MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v1/items/%d", itemID), body)
			}
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)

			var updated map[string]interface{}
			DecodeJSON(t, response, &updated)
			description, _ := updated["description"].(string)
			if updated["title"] != wantTitle || description != wantDescription {
				t.Fatalf("%s update changed source = %v, want title %q description %q", surface, updated, wantTitle, wantDescription)
			}
			rendered, _ := updated["description_html"].(string)
			if strings.Contains(rendered, "<script>") || strings.Contains(rendered, "javascript:") || !strings.Contains(rendered, "&lt;script&gt;") || !strings.Contains(rendered, "<br>") {
				t.Fatalf("%s description_html is not safe rendered Markdown: %q", surface, rendered)
			}

			var mentionCount int
			if err := server.server.DB().QueryRow(`
				SELECT COUNT(*) FROM mentions
				WHERE source_type = 'item_description' AND source_id = ? AND mentioned_user_id = ?
			`, itemID, mentionedUserID).Scan(&mentionCount); err != nil {
				t.Fatalf("%s load mention: %v", surface, err)
			}
			if mentionCount != 1 {
				t.Fatalf("%s item-description mentions = %d, want 1", surface, mentionCount)
			}
		})
	}
}
