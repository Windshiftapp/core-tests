package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestCustomWidgetDataHTTP exercises the custom widget data endpoint through
// the production API: a saved widget resolves its CQL slot with the viewer's
// permissions, returns normalized rows without heavy fields, and rejects an
// unknown widget.
func TestCustomWidgetDataHTTP(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Custom widget data "+ts(), shortKey("CWD"))
	itemTitle := "Widget data item " + ts()
	CreateTestItem(t, server, workspaceID, itemTitle)

	program := `root = Card([ItemsTable("status_completed = false", "key,title,status")])`
	queries := map[string]any{
		"item": map[string]any{"ql": "status_completed = false", "sort": "-updated_at", "limit": 50},
	}

	var widgetID int

	t.Run("create a widget with an item slot", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets", map[string]any{
			"name":         "Data widget",
			"program":      program,
			"queries":      queries,
			"workspace_id": nil,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var widget struct {
			ID      int             `json:"id"`
			Queries json.RawMessage `json:"queries"`
		}
		DecodeJSON(t, resp, &widget)
		if widget.ID == 0 {
			t.Fatal("created widget has no id")
		}
		widgetID = widget.ID
	})

	t.Run("resolve returns the matching item and no heavy fields", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/data", map[string]any{
			"widget_id": widgetID,
			"slots": []map[string]any{{
				"key": "k1", "entity": "item", "ql": "status_completed = false", "limit": 50,
			}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		raw := string(body)
		if strings.Contains(raw, "description") {
			t.Fatalf("response leaked a description field: %s", raw)
		}

		var decoded struct {
			Results map[string]struct {
				Status string `json:"status"`
				Rows   []struct {
					ID    int    `json:"id"`
					Title string `json:"title"`
					Key   string `json:"key"`
				} `json:"rows"`
				Total     int  `json:"total"`
				Truncated bool `json:"truncated"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			t.Fatalf("decode: %v; body=%s", err, raw)
		}

		result := decoded.Results["k1"]
		if result.Status != "ready" {
			t.Fatalf("status = %q, want ready; body=%s", result.Status, raw)
		}
		if result.Total < 1 {
			t.Fatalf("total = %d, want at least the created item", result.Total)
		}
		found := false
		for _, row := range result.Rows {
			if row.Title == itemTitle {
				found = true
				if row.Key == "" {
					t.Fatalf("row %+v has no display key", row)
				}
			}
		}
		if !found {
			t.Fatalf("rows did not contain %q: %+v", itemTitle, result.Rows)
		}
	})

	t.Run("a slot that matches nothing returns an empty ready result", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/data", map[string]any{
			"widget_id": widgetID,
			"slots": []map[string]any{{
				"key": "none", "entity": "item", "ql": `title = "no such widget data item"`, "limit": 5,
			}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var decoded struct {
			Results map[string]struct {
				Status string            `json:"status"`
				Rows   []json.RawMessage `json:"rows"`
				Total  int               `json:"total"`
			} `json:"results"`
		}
		DecodeJSON(t, resp, &decoded)
		result := decoded.Results["none"]
		if result.Status != "ready" || len(result.Rows) != 0 || result.Total != 0 {
			t.Fatalf("result = %+v, want an empty ready result", result)
		}
	})

	t.Run("an unknown widget id is rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/data", map[string]any{
			"widget_id": 999999,
			"slots":     []map[string]any{{"key": "k1", "entity": "item"}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("an edge slot resolves a bipartite graph", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/data", map[string]any{
			"widget_id": widgetID,
			"slots": []map[string]any{{
				"key":    "graph",
				"entity": "links",
				"links": map[string]any{
					"item":  map[string]any{"ql": "status_completed = false", "limit": 100},
					"asset": map[string]any{"asset_set_id": 1, "limit": 100},
				},
			}},
		})
		defer resp.Body.Close()

		// The asset set may not exist for this workspace, in which case the slot
		// reports a per-slot error rather than failing the request.
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
		}

		var decoded struct {
			Results map[string]struct {
				Status string `json:"status"`
				Graph  *struct {
					Nodes  []json.RawMessage `json:"nodes"`
					Edges  []json.RawMessage `json:"edges"`
					Counts struct {
						Items  int `json:"items"`
						Assets int `json:"assets"`
						Edges  int `json:"edges"`
					} `json:"counts"`
				} `json:"graph"`
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"results"`
		}
		DecodeJSON(t, resp, &decoded)

		result := decoded.Results["graph"]
		if result.Status == "ready" {
			if result.Graph == nil {
				t.Fatal("a ready edge slot must carry a graph")
			}
			if result.Graph.Counts.Items < 1 {
				t.Fatalf("counts = %+v, want the matching item in the seed set", result.Graph.Counts)
			}
		} else if result.Error == nil {
			t.Fatalf("result = %+v, want either a graph or a per-slot error", result)
		}
	})

	t.Run("an edge slot without both seeds is rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/data", map[string]any{
			"slots": []map[string]any{{
				"key": "graph", "entity": "links",
				"links": map[string]any{"item": map[string]any{"limit": 10}},
			}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("an asset slot without a set is rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/data", map[string]any{
			"slots": []map[string]any{{"key": "k1", "entity": "asset"}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})
}
