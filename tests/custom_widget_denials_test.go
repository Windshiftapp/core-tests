package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestCustomWidgetDataDenials pins the authorization boundary of the widget
// data endpoint from the outside.
//
// Note on workspace visibility: a workspace with no explicit role assignment is
// open to every logged-in user by design (see the permission cache builder's
// "everyone" set), so an item query against such a workspace legitimately
// returns rows. The restricted-workspace denial is covered where it can be set
// up precisely — internal/handlers TestCustomWidgetDataScopesSavedWorkspaceWidget
// seeds an explicitly role-restricted workspace and asserts both the 404 and
// that no query is issued at all.
func TestCustomWidgetDataDenials(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Widget denial "+ts(), shortKey("WDN"))
	itemTitle := "Widget denial item " + ts()
	CreateTestItem(t, server, workspaceID, itemTitle)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	_, username, password := CreateTestUserWithCredentials(t, server, "wd-"+suffix, "wd-"+suffix+"@example.test")
	outsiderCookie := CreateBearerTokenForUser(t, server, username, password)

	readBody := func(t *testing.T, resp *http.Response) string {
		t.Helper()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return string(body)
	}

	t.Run("an asset query for a set the viewer cannot read is denied per slot", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, outsiderCookie, http.MethodPost, "/custom-widgets/data", map[string]any{
			"slots": []map[string]any{{
				"key": "assets", "entity": "asset", "asset_set_id": 987654, "limit": 50,
			}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var decoded struct {
			Results map[string]struct {
				Status string `json:"status"`
				Rows   []any  `json:"rows"`
				Error  *struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"results"`
		}
		DecodeJSON(t, resp, &decoded)

		result := decoded.Results["assets"]
		if result.Status != "error" || result.Error == nil {
			t.Fatalf("result = %+v, want a per-slot denial", result)
		}
		if len(result.Rows) != 0 {
			t.Fatalf("rows = %d, want none for an inaccessible asset set", len(result.Rows))
		}
	})

	t.Run("a denied slot does not stop the rest of the request", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, outsiderCookie, http.MethodPost, "/custom-widgets/data", map[string]any{
			"slots": []map[string]any{
				{"key": "assets", "entity": "asset", "asset_set_id": 987654, "limit": 50},
				{"key": "items", "entity": "item", "ql": "status_completed = false", "limit": 50},
			},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var decoded struct {
			Results map[string]struct {
				Status string            `json:"status"`
				Rows   []json.RawMessage `json:"rows"`
			} `json:"results"`
		}
		DecodeJSON(t, resp, &decoded)

		if decoded.Results["assets"].Status != "error" {
			t.Fatalf("asset slot = %+v, want an error", decoded.Results["assets"])
		}
		if decoded.Results["items"].Status != "ready" {
			t.Fatalf("item slot = %+v, want the other slot to still resolve", decoded.Results["items"])
		}
	})

	t.Run("an inaccessible widget is refused before any query runs", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, outsiderCookie, http.MethodPost, "/custom-widgets/data", map[string]any{
			"widget_id": 424242,
			"slots":     []map[string]any{{"key": "k", "entity": "item", "limit": 5}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("the viewer still resolves their own item query", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/data", map[string]any{
			"workspace_id": workspaceID,
			"slots":        []map[string]any{{"key": "k", "entity": "item", "limit": 50}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		if !strings.Contains(readBody(t, resp), itemTitle) {
			t.Fatal("the workspace owner should see the item they created")
		}
	})
}
