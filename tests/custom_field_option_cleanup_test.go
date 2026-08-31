package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"windshift/internal/scheduler"
)

// TestCustomFieldOptionDeletionCleansItems documents the Q3 invariant from
// docs/testhunt1.md: when a select/multiselect custom field's options are
// edited and an option is removed, the backend scrubs any stored references to
// that option from items' cfv JSON.
//
//   - select: the field entry is removed from cfv (item appears "not set")
//   - multiselect: the deleted option id is filtered out; if the array
//     becomes empty, the field is removed entirely
//
// The scrub is now asynchronous (WI-419): the Update handler enqueues an
// option_removal job and CFVCleanupScheduler drains it in bounded batches.
// updateCustomFieldOptions drives one scheduler tick so these tests assert the
// post-drain state. The scrub is best-effort: enqueue errors are logged but
// don't block the field update.
func TestCustomFieldOptionDeletionCleansItems(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Option Cleanup WS", shortKey("OCWS"))

	t.Run("select_field_orphan_is_removed_from_item_cfv", func(t *testing.T) {
		// Create a select field with three options.
		initialOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "Low"},
			{ID: 2, Label: "Medium"},
			{ID: 3, Label: "High"},
		})
		fieldID := CreateTestCustomField(t, server, "Priority "+ts(), "select", initialOpts)
		fieldKey := strconv.Itoa(fieldID)

		// Create an item that selects option id=2 (Medium).
		itemID := createItemWithCFV(t, server, workspaceID, "Item with priority "+ts(), map[string]interface{}{
			fieldKey: 2,
		})

		// Sanity: server stored the value.
		gotBefore := getItemCFV(t, server, itemID)
		if v, ok := gotBefore[fieldKey].(float64); !ok || int(v) != 2 {
			t.Fatalf("precondition: expected cfv[%s]=2, got %v", fieldKey, gotBefore[fieldKey])
		}

		// Update the field, dropping option id=2.
		updatedOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "Low"},
			{ID: 3, Label: "High"},
		})
		updateCustomFieldOptions(t, server, fieldID, "select", "Priority", updatedOpts)

		// After cleanup, the field entry must be absent from the item's cfv.
		gotAfter := getItemCFV(t, server, itemID)
		if _, present := gotAfter[fieldKey]; present {
			t.Errorf("expected cfv[%s] to be removed after the selected option was deleted, but it is still %v", fieldKey, gotAfter[fieldKey])
		}
	})

	t.Run("select_field_still_valid_option_is_preserved", func(t *testing.T) {
		// Inverse case: an item that selected an option NOT being deleted
		// must keep its value through the cleanup.
		initialOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "Low"},
			{ID: 2, Label: "Medium"},
			{ID: 3, Label: "High"},
		})
		fieldID := CreateTestCustomField(t, server, "Severity "+ts(), "select", initialOpts)
		fieldKey := strconv.Itoa(fieldID)

		itemID := createItemWithCFV(t, server, workspaceID, "Item with severity "+ts(), map[string]interface{}{
			fieldKey: 3, // High — will survive
		})

		// Drop option id=1 (which this item doesn't use).
		updatedOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 2, Label: "Medium"},
			{ID: 3, Label: "High"},
		})
		updateCustomFieldOptions(t, server, fieldID, "select", "Severity", updatedOpts)

		gotAfter := getItemCFV(t, server, itemID)
		v, ok := gotAfter[fieldKey].(float64)
		if !ok || int(v) != 3 {
			t.Errorf("expected cfv[%s]=3 to survive cleanup, got %v", fieldKey, gotAfter[fieldKey])
		}
	})

	t.Run("multiselect_field_orphan_id_is_filtered_from_array", func(t *testing.T) {
		initialOpts := mustSerializeOptions(t, 5, []selectOption{
			{ID: 1, Label: "Bug"},
			{ID: 2, Label: "Feature"},
			{ID: 3, Label: "Tech debt"},
			{ID: 4, Label: "Spike"},
		})
		fieldID := CreateTestCustomField(t, server, "Tags "+ts(), "multiselect", initialOpts)
		fieldKey := strconv.Itoa(fieldID)

		// Item selects ids [1, 3, 4].
		itemID := createItemWithCFV(t, server, workspaceID, "Tagged item "+ts(), map[string]interface{}{
			fieldKey: []int{1, 3, 4},
		})

		// Remove option id=3.
		updatedOpts := mustSerializeOptions(t, 5, []selectOption{
			{ID: 1, Label: "Bug"},
			{ID: 2, Label: "Feature"},
			{ID: 4, Label: "Spike"},
		})
		updateCustomFieldOptions(t, server, fieldID, "multiselect", "Tags", updatedOpts)

		// After cleanup, the stored array should be [1, 4] (order preserved
		// minus the deleted id).
		gotAfter := getItemCFV(t, server, itemID)
		arr, ok := gotAfter[fieldKey].([]interface{})
		if !ok {
			t.Fatalf("expected cfv[%s] to remain an array, got %T (%v)", fieldKey, gotAfter[fieldKey], gotAfter[fieldKey])
		}
		gotIDs := make([]int, 0, len(arr))
		for _, v := range arr {
			if n, ok := v.(float64); ok {
				gotIDs = append(gotIDs, int(n))
			}
		}
		want := []int{1, 4}
		if !equalIntSlices(gotIDs, want) {
			t.Errorf("expected cfv[%s]=%v after deleting option 3, got %v", fieldKey, want, gotIDs)
		}
	})

	t.Run("multiselect_field_becomes_empty_when_only_selected_option_is_deleted", func(t *testing.T) {
		// Edge case: when every selected option is removed, the renderer
		// already shows an empty array as "1 linked" — and the cleanup
		// deletes the field entry entirely (cfv[fieldKey] is removed).
		// This documents the latter: the field key is GONE from cfv,
		// not just set to [].
		initialOpts := mustSerializeOptions(t, 3, []selectOption{
			{ID: 1, Label: "Red"},
			{ID: 2, Label: "Blue"},
		})
		fieldID := CreateTestCustomField(t, server, "Color "+ts(), "multiselect", initialOpts)
		fieldKey := strconv.Itoa(fieldID)

		itemID := createItemWithCFV(t, server, workspaceID, "Colored item "+ts(), map[string]interface{}{
			fieldKey: []int{1, 2},
		})

		// Remove both options. Note: the API doesn't allow empty options
		// on a select/multiselect, so we leave one option but it's a
		// fresh new id (3) that no item references.
		updatedOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 3, Label: "Green"},
		})
		updateCustomFieldOptions(t, server, fieldID, "multiselect", "Color", updatedOpts)

		gotAfter := getItemCFV(t, server, itemID)
		if _, present := gotAfter[fieldKey]; present {
			t.Errorf("expected cfv[%s] to be absent after every selected option was deleted, got %v", fieldKey, gotAfter[fieldKey])
		}
	})
}

// --- helpers ---------------------------------------------------------------

type selectOption struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

func mustSerializeOptions(t *testing.T, nextID int, items []selectOption) string {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{"next_id": nextID, "items": items})
	if err != nil {
		t.Fatalf("marshal options: %v", err)
	}
	return string(b)
}

func ts() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// updateCustomFieldOptions PUTs the field with a new options payload. The
// payload must include the canonical fields the handler reads — name,
// field_type, options — otherwise validation rejects it.
func updateCustomFieldOptions(t *testing.T, server *TestServer, fieldID int, fieldType, name, newOptions string) {
	t.Helper()
	body := map[string]interface{}{
		"name":       name,
		"field_type": fieldType,
		"required":   false,
		"options":    newOptions,
	}
	resp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/admin/custom-fields/%d", fieldID), body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	// Option-removal cleanup is async (WI-419): the PUT only enqueues the job.
	// Drain one scheduler tick so callers observe the scrubbed state. A no-op
	// when the edit removed no options (e.g. a rename).
	scheduler.NewCFVCleanupScheduler(server.DB()).RunOnceForTests()
}

// createItemWithCFV creates a work item with the given custom_field_values
// payload and returns its id. Uses a wide-net set of required-shape fields
// to satisfy the items handler (workspace, item_type, etc.).
func createItemWithCFV(t *testing.T, server *TestServer, workspaceID int, title string, cfv map[string]interface{}) int {
	t.Helper()

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)

	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	body := map[string]interface{}{
		"title":               title,
		"workspace_id":        workspaceID,
		"item_type_id":        itemTypeID,
		"custom_field_values": cfv,
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, "/items", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}

// getItemCFV fetches the item and returns its custom_field_values map.
func getItemCFV(t *testing.T, server *TestServer, itemID int) map[string]interface{} {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var item map[string]interface{}
	DecodeJSON(t, resp, &item)
	cfv, ok := item["custom_field_values"].(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return cfv
}

// (equalIntSlices is provided by wscli_smoke_test.go in this package.)
