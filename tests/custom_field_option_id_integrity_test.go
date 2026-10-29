package tests

import (
	"net/http"
	"strconv"
	"testing"
)

// WI-1428: select and multiselect option ids are integers. A fractional JSON
// number must be rejected with 400 instead of being truncated into a valid id,
// and an accepted value must persist as its integer id.
func TestCustomFieldOptionIDsRejectFractionalValues(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Option ID WS", shortKey("OIWS"))

	options := mustSerializeOptions(t, 4, []selectOption{
		{ID: 1, Label: "Low"},
		{ID: 2, Label: "High"},
	})
	selectID := CreateTestCustomField(t, server, "Select "+ts(), "select", options)
	multiID := CreateTestCustomField(t, server, "Multi "+ts(), "multiselect", options)

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	create := func(t *testing.T, title string, cfv map[string]interface{}) *http.Response {
		t.Helper()
		return MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
			"title":               title,
			"workspace_id":        workspaceID,
			"item_type_id":        itemTypeID,
			"custom_field_values": cfv,
		})
	}

	t.Run("select fractional option id is rejected", func(t *testing.T) {
		resp := create(t, "Fractional select "+ts(), map[string]interface{}{
			strconv.Itoa(selectID): 1.5,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("multiselect fractional option id is rejected", func(t *testing.T) {
		resp := create(t, "Fractional multiselect "+ts(), map[string]interface{}{
			strconv.Itoa(multiID): []interface{}{1.5},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("integral option id is accepted and stored as an integer", func(t *testing.T) {
		key := strconv.Itoa(selectID)
		itemID := createItemWithCFV(t, server, workspaceID, "Integral select "+ts(), map[string]interface{}{
			key: 2,
		})

		got := getItemCFV(t, server, itemID)
		if v, ok := got[key].(float64); !ok || v != 2 {
			t.Fatalf("stored select value = %#v (%T), want 2", got[key], got[key])
		}
	})
}
