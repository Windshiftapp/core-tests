package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// TestV1SearchCQL exercises the v1 bearer-token search endpoint
// (GET /rest/api/v1/search/items) — the surface the `ws` CLI uses. It must
// support structured CQL filtering (e.g. milestone = '0.8.2') in addition to
// full-text search:
//   - an explicit `ql` parameter forces CQL and surfaces parse errors;
//   - a `q` that parses as a CQL filter is auto-detected and evaluated as CQL;
//   - a `q` that is plain text still falls through to full-text matching.
func TestV1SearchCQL(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "CQL Search Test", "CQLS")
	categoryIDs := CreateTestStatusCategories(t, server, "CQLS")
	statusIDs := CreateTestStatuses(t, server, "CQLS", categoryIDs)
	// Create-time status overrides are workflow-validated — bind the fresh
	// statuses to this workspace before seeding items in them.
	BindStatusesToWorkspace(t, server, workspaceID, "CQL Search Workflow", statusIDs)
	statusOpen := statusIDs[0]
	itemTypes := GetItemTypes(t, server, GetDefaultConfigurationSet(t, server))
	bugTypeID := RequireItemTypeID(t, itemTypes, "Bug")

	// Workspace-scoped milestone the CQL filter will target.
	milestoneData := map[string]interface{}{
		"name":         "0.8.2",
		"description":  "Search CQL milestone",
		"status":       "in-progress",
		"workspace_id": workspaceID,
	}
	msResp := MakeAuthRequest(t, server, http.MethodPost, "/milestones", milestoneData)
	AssertStatusCode(t, msResp, http.StatusCreated)
	var msResult map[string]interface{}
	DecodeJSON(t, msResp, &msResult)
	msResp.Body.Close()

	createItem := func(title string, inMilestone bool) int {
		t.Helper()
		data := map[string]interface{}{
			"workspace_id": workspaceID,
			"title":        title,
			"status_id":    statusOpen,
		}
		if inMilestone {
			// Create takes bare ids (milestone_ids); `milestones` is the read shape.
			data["milestone_ids"] = []int{ExtractIDFromResponse(t, msResult)}
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/items", data)
		AssertStatusCode(t, resp, http.StatusCreated)
		var r map[string]interface{}
		DecodeJSON(t, resp, &r)
		resp.Body.Close()
		return ExtractIDFromResponse(t, r)
	}

	inMilestoneID := createItem("Zephyr widget in milestone", true)
	createItem("Zephyr widget unscheduled", false)
	bugItemResp := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
		"workspace_id": workspaceID,
		"item_type_id": bugTypeID,
		"title":        "Zephyr bug widget",
		"status_id":    statusOpen,
	})
	AssertStatusCode(t, bugItemResp, http.StatusCreated)
	var bugItem map[string]interface{}
	DecodeJSON(t, bugItemResp, &bugItem)
	bugItemResp.Body.Close()
	bugItemID := ExtractIDFromResponse(t, bugItem)

	// search hits the v1 bearer search endpoint and returns the item ids.
	search := func(t *testing.T, param, value string) (*http.Response, []int) {
		t.Helper()
		path := fmt.Sprintf("/rest/api/v1/search/items?%s=%s&limit=100", param, url.QueryEscape(value))
		resp := MakeBearerRequest(t, server, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusOK {
			return resp, nil
		}
		var body struct {
			Data []struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		DecodeJSON(t, resp, &body)
		ids := make([]int, 0, len(body.Data))
		for _, it := range body.Data {
			ids = append(ids, it.ID)
		}
		return resp, ids
	}

	containsOnly := func(t *testing.T, ids []int, want int) {
		t.Helper()
		if len(ids) != 1 || ids[0] != want {
			t.Fatalf("expected exactly item %d, got %v", want, ids)
		}
	}

	t.Run("explicit ql milestone filter", func(t *testing.T) {
		resp, ids := search(t, "ql", "milestone = '0.8.2'")
		AssertStatusCode(t, resp, http.StatusOK)
		containsOnly(t, ids, inMilestoneID)
	})

	t.Run("q auto-detected as cql", func(t *testing.T) {
		resp, ids := search(t, "q", "milestone = '0.8.2'")
		AssertStatusCode(t, resp, http.StatusOK)
		containsOnly(t, ids, inMilestoneID)
	})

	t.Run("compound cql filter", func(t *testing.T) {
		resp, ids := search(t, "q", `milestone = '0.8.2' AND title ~ "Zephyr"`)
		AssertStatusCode(t, resp, http.StatusOK)
		containsOnly(t, ids, inMilestoneID)
	})

	t.Run("type alias accepts an unquoted item type name", func(t *testing.T) {
		resp, ids := search(t, "q", "type = bUg")
		AssertStatusCode(t, resp, http.StatusOK)
		containsOnly(t, ids, bugItemID)
	})

	t.Run("itemtype accepts an unquoted item type name", func(t *testing.T) {
		resp, ids := search(t, "ql", "itemtype = bug")
		AssertStatusCode(t, resp, http.StatusOK)
		containsOnly(t, ids, bugItemID)
	})

	t.Run("plain text still full-text matches", func(t *testing.T) {
		// "unscheduled" is plain text (no operator) → full-text path, matches
		// the title of the non-milestone item only.
		resp, ids := search(t, "q", "unscheduled")
		AssertStatusCode(t, resp, http.StatusOK)
		if len(ids) != 1 || ids[0] == inMilestoneID {
			t.Fatalf("expected the unscheduled item only, got %v (milestone item %d)", ids, inMilestoneID)
		}
	})

	t.Run("invalid ql returns 400", func(t *testing.T) {
		resp, _ := search(t, "ql", "status =")
		AssertStatusCode(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	})

	t.Run("missing q and ql returns 400", func(t *testing.T) {
		resp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v1/search/items", nil)
		AssertStatusCode(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	})
}
