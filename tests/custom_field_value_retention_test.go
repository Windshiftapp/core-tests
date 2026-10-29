//go:build test

package tests

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// Item updates merge custom_field_values per field server-side: payload keys
// set their field, a null value clears it, keys left out of the payload are
// preserved, and a null payload is ignored. These tests pin the wire contract
// every client relies on — first-party callers (list-view inline edit, ws
// task edit, the update_item AI tool) send exactly the fields they edit and
// the server preserves the rest.
func TestCustomFieldValueRetentionOnUpdate(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "CFV Retention WS "+ts(), shortKey("CFVR"))

	fieldA := CreateTestCustomField(t, server, "Retention text "+ts(), "text", "")
	fieldB := CreateTestCustomField(t, server, "Retention number "+ts(), "number", "")
	fieldC := CreateTestCustomField(t, server, "Retention added "+ts(), "text", "")
	keyA, keyB, keyC := strconv.Itoa(fieldA), strconv.Itoa(fieldB), strconv.Itoa(fieldC)

	itemID := createItemWithCFV(t, server, workspaceID, "Retention item "+ts(), map[string]interface{}{
		keyA: "original text",
		keyB: 7.5,
	})

	patchCFV := func(t *testing.T, cfv map[string]interface{}) map[string]interface{} {
		t.Helper()
		resp := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/items/%d", itemID), map[string]interface{}{
			"custom_field_values": cfv,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		return getItemCFV(t, server, itemID)
	}

	t.Run("partial patch preserves the item's other fields", func(t *testing.T) {
		got := patchCFV(t, map[string]interface{}{
			keyC: "newly edited field",
		})
		if got[keyA] != "original text" {
			t.Fatalf("custom_field_values[%q] = %v, want original text", keyA, got[keyA])
		}
		if got[keyB] != 7.5 {
			t.Fatalf("custom_field_values[%q] = %v, want 7.5", keyB, got[keyB])
		}
		if got[keyC] != "newly edited field" {
			t.Fatalf("custom_field_values[%q] = %v, want newly edited field", keyC, got[keyC])
		}
	})

	t.Run("a full map sets every submitted key", func(t *testing.T) {
		got := patchCFV(t, map[string]interface{}{
			keyA: "rewritten text",
			keyB: 9.0,
			keyC: "kept",
		})
		if got[keyA] != "rewritten text" || got[keyB] != 9.0 || got[keyC] != "kept" {
			t.Fatalf("custom_field_values = %v, want all three keys updated", got)
		}
	})

	t.Run("null value clears one field and preserves the rest", func(t *testing.T) {
		got := patchCFV(t, map[string]interface{}{
			keyC: nil,
		})
		if _, present := got[keyC]; present {
			t.Fatalf("custom_field_values[%q] = %v, want the field cleared", keyC, got[keyC])
		}
		if got[keyA] != "rewritten text" || got[keyB] != 9.0 {
			t.Fatalf("custom_field_values = %v, want the other fields preserved", got)
		}
	})

	t.Run("null payload is ignored instead of wiping the blob", func(t *testing.T) {
		// A nil interface marshals to JSON null: {"custom_field_values": null}.
		resp := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/items/%d", itemID), map[string]interface{}{
			"custom_field_values": nil,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		got := getItemCFV(t, server, itemID)
		if got[keyA] != "rewritten text" || got[keyB] != 9.0 {
			t.Fatalf("custom_field_values after null payload = %v, want both fields preserved", got)
		}
	})
}
