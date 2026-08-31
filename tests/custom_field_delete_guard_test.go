package tests

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCustomFieldDeleteGuard pins the in-use delete guard on custom fields: a
// field that still holds a value anywhere — an item's cfv JSON, an asset's cfv
// JSON, or the portal custom_field_values table — cannot be deleted. The Delete
// handler returns 409 and leaves the field intact, mirroring the item-type
// guard. Once the referencing values are gone the field deletes normally.
//
// Before this guard, delete succeeded unconditionally and an async job scrubbed
// the orphan keys later (see TestCFVCleanupScheduler); the scrub path now only
// fires on the concurrent-write race, so it is exercised via a direct enqueue.
func TestCustomFieldDeleteGuard(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "CF Delete Guard WS", shortKey("CFDG"))

	t.Run("blocks_delete_while_an_item_holds_a_value_then_allows_after_clearing", func(t *testing.T) {
		fieldID := CreateTestCustomField(t, server, "Guarded Item Field "+ts(), "text", "")
		fieldKey := strconv.Itoa(fieldID)

		itemID := createItemWithCFV(t, server, workspaceID, "Guarded item "+ts(),
			map[string]interface{}{fieldKey: "in use"})

		// In use -> 409 with an explanatory message; the field is NOT deleted.
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", fieldID), nil)
		AssertStatusCode(t, resp, http.StatusConflict)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(body), "Cannot delete custom field") {
			t.Errorf("expected an explanatory conflict message, got: %s", string(body))
		}

		// Clear the only reference by deleting the item, then the field deletes.
		// The 204 here also proves the field survived the earlier 409.
		delItem := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/items/%d", itemID), nil)
		_ = delItem.Body.Close()
		AssertStatusCode(t, delItem, http.StatusNoContent)

		delField := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", fieldID), nil)
		_ = delField.Body.Close()
		AssertStatusCode(t, delField, http.StatusNoContent)
	})

	t.Run("blocks_delete_while_a_portal_custom_field_value_exists", func(t *testing.T) {
		// The portal/org surface stores values in the separate (legacy)
		// custom_field_values table, keyed by a real custom_field_id column
		// rather than as cfv JSON on items/assets. That table is not part of the
		// fresh schema — it only lingers on older deployments — and
		// CountRowsUsingField tolerates its absence (the unused-field subtest
		// pins that path). Recreate it here to exercise the branch that counts it.
		if _, err := server.DB().ExecWrite(
			`CREATE TABLE IF NOT EXISTS custom_field_values (
				item_id INTEGER, custom_field_id INTEGER, value TEXT,
				created_at TIMESTAMP, updated_at TIMESTAMP)`,
		); err != nil {
			t.Fatalf("create legacy custom_field_values table: %v", err)
		}
		t.Cleanup(func() { _, _ = server.DB().ExecWrite(`DROP TABLE IF EXISTS custom_field_values`) })

		fieldID := CreateTestCustomField(t, server, "Guarded Portal Field "+ts(), "text", "")

		now := time.Now()
		if _, err := server.DB().ExecWrite(
			`INSERT INTO custom_field_values (item_id, custom_field_id, value, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)`,
			1, fieldID, "portal value", now, now,
		); err != nil {
			t.Fatalf("seed portal custom_field_values row: %v", err)
		}

		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", fieldID), nil)
		_ = resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)
	})

	t.Run("allows_delete_of_an_unused_field", func(t *testing.T) {
		fieldID := CreateTestCustomField(t, server, "Unused Field "+ts(), "text", "")

		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", fieldID), nil)
		_ = resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)
	})
}
