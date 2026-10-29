package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCustomFieldDeleteGuardIgnoresMatchingStringValues pins the WI-1160 fix:
// the delete guard counts only rows whose CFV JSON actually carries the field
// ID as a KEY. A text field's VALUE equal to another field's numeric id (e.g.
// a text field holding "12") must not block deleting custom field 12. Under
// the old quoted-ID LIKE prefilter, such rows matched the pattern and the 409
// blocked deletion indefinitely with no escape hatch.
func TestCustomFieldDeleteGuardIgnoresMatchingStringValues(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "CF Guard Value WS", shortKey("CFGV"))

	// Field 12 will be deleted; field 13 is a text field whose VALUE happens
	// to be the digits of field 12's id.
	targetID := CreateTestCustomField(t, server, "Guard Target "+ts(), "number", "")
	otherID := CreateTestCustomField(t, server, "TextWithValue "+ts(), "text", "")

	// Field IDs are small in a fresh DB; force determinism by checking the
	// invariant the test relies on: the two ids must differ, and the text
	// value must equal the TARGET's id, not the text field's own id.
	if targetID == otherID {
		t.Fatalf("fixture drift: field ids collide (%d)", targetID)
	}

	itemID := createItemWithCFV(t, server, workspaceID, "Item with numeric-looking text "+ts(), map[string]interface{}{
		fmt.Sprintf("%d", otherID): fmt.Sprintf("%d", targetID),
	})
	_ = itemID

	// The guard must see through the LIKE pattern: no row uses the target
	// field as a KEY, so the delete succeeds (204) — under the old code this
	// returned 409 and could never succeed.
	delResp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", targetID), nil)
	defer delResp.Body.Close()
	AssertStatusCode(t, delResp, http.StatusNoContent)

	// The text field and its value-bearing item are untouched.
	itemResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
	defer itemResp.Body.Close()
	AssertStatusCode(t, itemResp, http.StatusOK)
}

// TestCustomFieldDeleteGuardStillBlocksRealUsage keeps the positive guard
// contract pinned next to the WI-1160 fix: a row holding the field ID as a
// genuine KEY still blocks deletion with 409.
func TestCustomFieldDeleteGuardStillBlocksRealUsage(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "CF Guard Key WS", shortKey("CFGK"))
	fieldID := CreateTestCustomField(t, server, "Really Used "+ts(), "text", "")

	createItemWithCFV(t, server, workspaceID, "Item using the field "+ts(), map[string]interface{}{
		fmt.Sprintf("%d", fieldID): "actual value",
	})

	resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", fieldID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusConflict)
}
