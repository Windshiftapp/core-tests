package tests

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"windshift/internal/scheduler"
)

// TestCustomFieldEdgeCases groups three related backend invariants that
// were surfaced during the testhunt #1 review. Each sub-test pins current
// behavior so any future change either intentionally flips the assertion
// or fails the test as a regression.
//
// Findings these tests pin:
//
//  1. Deleting a custom field that still holds values is blocked with 409
//     (the in-use delete guard, mirroring item types). The admin must clear
//     the values first; the async cfv scrub remains only as defense-in-depth
//     for the concurrent-write race (see TestCustomFieldDeleteGuard and
//     TestCFVCleanupScheduler).
//
//  2. POST /items accepts arbitrary option ids for select/multiselect
//     fields without checking them against the field's option set. The
//     value is stored verbatim; the renderer's resolveOptionLabel
//     safety net turns unknown ids into raw strings.
//
//  3. Renaming an option's label (id stable) propagates to existing items
//     automatically because items store ids — not labels. This is the
//     happy-path invariant of the id-based options format; pinning it
//     here breaks any future "store labels too" regression loudly.
func TestCustomFieldEdgeCases(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Edge Cases WS", shortKey("ECWS"))

	t.Run("field_deletion_is_blocked_while_items_still_reference_it", func(t *testing.T) {
		// A field that still holds values cannot be deleted: the Delete handler
		// refuses with 409 (the in-use guard, mirroring item types) so the admin
		// clears the values first rather than silently orphaning cfv keys. The
		// async scrub is no longer reachable via this path — see
		// TestCFVCleanupScheduler for the race-only drain coverage.
		fieldID := CreateTestCustomField(t, server, "Guarded Field "+ts(), "text", "")
		fieldKey := strconv.Itoa(fieldID)

		itemID := createItemWithCFV(t, server, workspaceID, "Item w/ guarded cfv "+ts(), map[string]interface{}{
			fieldKey: "transient value",
		})

		// Sanity check the value is present.
		gotBefore := getItemCFV(t, server, itemID)
		if v, _ := gotBefore[fieldKey].(string); v != "transient value" {
			t.Fatalf("precondition: cfv[%s] != %q (got %v)", fieldKey, "transient value", gotBefore[fieldKey])
		}

		// Delete is rejected while the item references the field.
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", fieldID), nil)
		_ = resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)

		// The field survives and the item's value is untouched.
		gotAfter := getItemCFV(t, server, itemID)
		if v, _ := gotAfter[fieldKey].(string); v != "transient value" {
			t.Errorf("expected cfv[%s] to be untouched after a blocked delete, got %v", fieldKey, gotAfter[fieldKey])
		}
	})

	t.Run("out_of_range_option_id_is_rejected_with_400", func(t *testing.T) {
		// items.go now validates select/multiselect ids against the field's
		// option set before storing cfv. A bogus id returns 400 Bad Request
		// with a message that includes the offending field key and id.
		initialOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "Low"},
			{ID: 2, Label: "Medium"},
			{ID: 3, Label: "High"},
		})
		fieldID := CreateTestCustomField(t, server, "Out-of-range Field "+ts(), "select", initialOpts)
		fieldKey := strconv.Itoa(fieldID)

		// POST /items with a bogus option id.
		configSetID := GetDefaultConfigurationSet(t, server)
		itemTypes := GetItemTypes(t, server, configSetID)
		itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

		resp := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
			"title":               "Out-of-range item " + ts(),
			"workspace_id":        workspaceID,
			"item_type_id":        itemTypeID,
			"custom_field_values": map[string]interface{}{fieldKey: 999},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("multiselect_with_invalid_id_is_rejected", func(t *testing.T) {
		initialOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "A"}, {ID: 2, Label: "B"},
		})
		fieldID := CreateTestCustomField(t, server, "MS Invalid Field "+ts(), "multiselect", initialOpts)
		fieldKey := strconv.Itoa(fieldID)

		configSetID := GetDefaultConfigurationSet(t, server)
		itemTypes := GetItemTypes(t, server, configSetID)
		itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

		resp := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
			"title":               "MS invalid item " + ts(),
			"workspace_id":        workspaceID,
			"item_type_id":        itemTypeID,
			"custom_field_values": map[string]interface{}{fieldKey: []int{1, 999}},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("cfv_for_nonexistent_field_id_is_accepted_and_stored", func(t *testing.T) {
		// Submitting cfv for a field id that doesn't exist — also accepted.
		// This is the same gap as field-deletion-orphans, but on the
		// create side: nothing checks fieldKey against the custom_fields
		// table before the JSON is stored.
		bogusKey := "99999"
		itemID := createItemWithCFV(t, server, workspaceID, "Bogus-key item "+ts(), map[string]interface{}{
			bogusKey: "ghost",
		})

		got := getItemCFV(t, server, itemID)
		if v, _ := got[bogusKey].(string); v != "ghost" {
			t.Errorf("expected stored cfv[%s]=%q (no schema validation), got %v", bogusKey, "ghost", got[bogusKey])
		}
	})

	t.Run("renaming_an_option_label_propagates_to_existing_items", func(t *testing.T) {
		// Items store option ids — not labels. So renaming label "Low" →
		// "Trivial" (keeping id=1) means an existing item with cfv = 1
		// should now display as "Trivial" the next time the renderer
		// runs. Since this is a backend test, we assert the cfv value
		// stays the same (id) and the option list reflects the new label.
		initialOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "Low"},
			{ID: 2, Label: "Medium"},
			{ID: 3, Label: "High"},
		})
		fieldID := CreateTestCustomField(t, server, "Renaming Field "+ts(), "select", initialOpts)
		fieldKey := strconv.Itoa(fieldID)

		itemID := createItemWithCFV(t, server, workspaceID, "Renaming item "+ts(), map[string]interface{}{
			fieldKey: 1, // Low
		})

		// Rename option id=1 to "Trivial". next_id stays at 4 (no new ids).
		renamedOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "Trivial"}, // <- relabel
			{ID: 2, Label: "Medium"},
			{ID: 3, Label: "High"},
		})
		updateCustomFieldOptions(t, server, fieldID, "select", "Renaming Field", renamedOpts)

		// Item's stored value must still be id=1 (id stability).
		got := getItemCFV(t, server, itemID)
		v, ok := got[fieldKey].(float64)
		if !ok || int(v) != 1 {
			t.Errorf("expected cfv[%s]=1 to survive a label rename, got %v", fieldKey, got[fieldKey])
		}

		// And the field's stored options reflect the new label.
		// (GET is at /custom-fields/{id}, not the admin path.)
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/custom-fields/%d", fieldID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var field map[string]interface{}
		DecodeJSON(t, resp, &field)
		optionsStr, _ := field["options"].(string)
		if !containsLabel(optionsStr, "Trivial") {
			t.Errorf("expected renamed label 'Trivial' to be in field.options, got %q", optionsStr)
		}
	})

	t.Run("multiselect_duplicate_ids_are_deduped_at_write_time", func(t *testing.T) {
		// The cfv validator dedupes multiselect arrays before storage so
		// the stored JSON is canonical. Order is preserved by first
		// occurrence.
		initialOpts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "Bug"},
			{ID: 2, Label: "Feature"},
		})
		fieldID := CreateTestCustomField(t, server, "Dup-id Field "+ts(), "multiselect", initialOpts)
		fieldKey := strconv.Itoa(fieldID)

		itemID := createItemWithCFV(t, server, workspaceID, "Dup-id item "+ts(), map[string]interface{}{
			fieldKey: []int{1, 1, 2, 1},
		})

		got := getItemCFV(t, server, itemID)
		arr, ok := got[fieldKey].([]interface{})
		if !ok {
			t.Fatalf("expected cfv[%s] to be an array, got %T", fieldKey, got[fieldKey])
		}
		var ids []int
		for _, v := range arr {
			if n, ok := v.(float64); ok {
				ids = append(ids, int(n))
			}
		}
		want := []int{1, 2}
		if !equalIntSlices(ids, want) {
			t.Errorf("expected deduped %v (first-seen order), got %v", want, ids)
		}
	})

	t.Run("unique_label_validation_rejects_create_with_duplicate_labels", func(t *testing.T) {
		// Two options with identical labels are indistinguishable in the
		// UI; the handler now rejects them at field-create/update time.
		dupOpts := mustSerializeOptions(t, 3, []selectOption{
			{ID: 1, Label: "Open"},
			{ID: 2, Label: "Open"},
		})
		body := map[string]interface{}{
			"name":       "Dup-label Field " + ts(),
			"field_type": "select",
			"required":   false,
			"options":    dupOpts,
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/custom-fields", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})
}

// runCFVCleanupOnce drives the cfv cleanup scheduler synchronously so
// tests can assert post-scrub state without waiting on the ticker. The
// helper uses a fresh scheduler bound to the test server's DB; the
// scheduler started by server.go is irrelevant to the test because it
// shares state via the table, not in-memory queues.
func runCFVCleanupOnce(t *testing.T, server *TestServer) {
	t.Helper()
	sch := scheduler.NewCFVCleanupScheduler(server.DB())
	sch.RunOnceForTests()
}

func containsLabel(optionsJSON, label string) bool {
	return optionsJSON != "" && (string(optionsJSON) != "" && containsAll(optionsJSON, []string{`"label":"` + label + `"`}))
}

func containsAll(s string, needles []string) bool {
	for _, n := range needles {
		if !contains(s, n) {
			return false
		}
	}
	return true
}

// contains is a strings.Contains shim kept private so this file is import-
// minimal alongside other tests in the same package.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
