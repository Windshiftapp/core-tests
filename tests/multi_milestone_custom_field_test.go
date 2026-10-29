package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"windshift/internal/scheduler"
)

// Multi-milestone custom fields store an array of milestone ids. CQL resolves
// both prefixed and bare field names and uses array-containment semantics; the
// dedupe and existence checks live in the shared custom-field validator.
func TestMultiMilestoneCustomField(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Multi Milestone WS", shortKey("MMWS"))

	first := createMilestoneForTest(t, server, "Affected 1.0 "+ts())
	second := createMilestoneForTest(t, server, "Affected 2.0 "+ts())

	fieldID := CreateTestCustomField(t, server, "affectedVersion", "multi_milestone", "")
	fieldKey := strconv.Itoa(fieldID)

	itemBoth := createItemWithCFV(t, server, workspaceID, "Affects 1.0 and 2.0 "+ts(), map[string]interface{}{
		fieldKey: []int{first, second},
	})
	itemSecond := createItemWithCFV(t, server, workspaceID, "Affects 2.0 "+ts(), map[string]interface{}{
		fieldKey: []int{second},
	})
	itemEmpty := createItemWithCFV(t, server, workspaceID, "Affects nothing "+ts(), map[string]interface{}{
		fieldKey: []int{},
	})

	t.Run("stores and returns the selected milestone ids", func(t *testing.T) {
		got := getItemCFV(t, server, itemBoth)
		values, ok := got[fieldKey].([]interface{})
		if !ok {
			t.Fatalf("cfv[%s] = %#v, want an array", fieldKey, got[fieldKey])
		}
		if len(values) != 2 || int(values[0].(float64)) != first || int(values[1].(float64)) != second {
			t.Fatalf("cfv[%s] = %#v, want [%d %d]", fieldKey, values, first, second)
		}
	})

	t.Run("bare CQL name matches a stored milestone", func(t *testing.T) {
		assertIDs(t, qlItemIDs(t, server, fmt.Sprintf("affectedVersion = %d", first)), itemBoth)
		assertIDs(t, qlItemIDs(t, server, fmt.Sprintf("cf_affectedVersion = %d", second)), itemBoth, itemSecond)
	})

	t.Run("CQL IN matches any selected milestone", func(t *testing.T) {
		assertIDs(t, qlItemIDs(t, server, fmt.Sprintf("affectedVersion IN (%d, %d)", first, second)), itemBoth, itemSecond)
	})

	t.Run("CQL not-equals excludes items that contain the milestone", func(t *testing.T) {
		assertIDs(t, qlItemIDs(t, server, fmt.Sprintf("affectedVersion != %d", first)), itemSecond, itemEmpty)
	})

	t.Run("CQL tolerates an item with an empty selection", func(t *testing.T) {
		assertIDs(t, qlItemIDs(t, server, fmt.Sprintf("affectedVersion = %d", first)), itemBoth)
		if ids := qlItemIDs(t, server, "affectedVersion IS NULL"); !containsID(ids, itemEmpty) {
			t.Fatalf("affectedVersion IS NULL = %v, want it to include the empty-selection item %d", ids, itemEmpty)
		}
	})

	t.Run("rejects a value that references a missing milestone", func(t *testing.T) {
		body := createItemRequestBody(t, server, workspaceID, "Invalid milestone "+ts(), map[string]interface{}{
			fieldKey: []int{999999},
		})
		resp := MakeAuthRequest(t, server, http.MethodPost, "/items", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("deleting a milestone strips it from stored arrays", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/v2/milestones/%d", first), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)

		scheduler.NewCFVCleanupScheduler(server.DB()).RunOnceForTests()

		got := getItemCFV(t, server, itemBoth)
		values, ok := got[fieldKey].([]interface{})
		if !ok || len(values) != 1 || int(values[0].(float64)) != second {
			t.Fatalf("cfv[%s] after milestone delete = %#v, want [%d]", fieldKey, got[fieldKey], second)
		}

		gotSecond := getItemCFV(t, server, itemSecond)
		valuesSecond, ok := gotSecond[fieldKey].([]interface{})
		if !ok || len(valuesSecond) != 1 || int(valuesSecond[0].(float64)) != second {
			t.Fatalf("unrelated item cfv[%s] = %#v, want [%d]", fieldKey, gotSecond[fieldKey], second)
		}
	})
}

func createMilestoneForTest(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/milestones", map[string]interface{}{
		"name":   name,
		"status": "planning",
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}

func createItemRequestBody(t *testing.T, server *TestServer, workspaceID int, title string, cfv map[string]interface{}) map[string]interface{} {
	t.Helper()
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	return map[string]interface{}{
		"title":               title,
		"workspace_id":        workspaceID,
		"item_type_id":        RequireItemTypeID(t, itemTypes, "Task"),
		"custom_field_values": cfv,
	}
}

func qlItemIDs(t *testing.T, server *TestServer, query string) []int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, "/items?ql="+url.QueryEscape(query)+"&limit=1000", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var page struct {
		Items []struct {
			ID int `json:"id"`
		} `json:"items"`
	}
	DecodeJSON(t, resp, &page)
	ids := make([]int, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func containsID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func assertIDs(t *testing.T, got []int, want ...int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("item ids = %v, want %v", got, want)
	}
	seen := make(map[int]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("item ids = %v, want %v", got, want)
		}
	}
}
