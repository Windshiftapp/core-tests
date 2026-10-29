package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Reproduction for: setting an asset-type custom field (e.g. "Component")
// during item creation loses the value, while setting the same field on the
// item detail screen persists it.
//
// The create form submits the picked asset as an object
// {"id": <assetID>, "title": ..., "asset_tag": ...} under custom_field_values;
// the detail screen submits the same shape via PATCH. These tests pin that both
// paths round-trip the value identically.
func TestItemCreateWithAssetCustomField(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	setID, typeID := seedAssetSetAndType(t, server, "cfv-create")
	assetID := createAssetForCFVTest(t, server, setID, typeID, "Component A")

	fieldID := CreateTestCustomField(t, server, "Component "+ts(), "asset", fmt.Sprintf(`{"asset_set_id":%d}`, setID))
	fieldKey := fmt.Sprintf("%d", fieldID)

	workspaceID, _ := CreateTestWorkspace(t, server, "Asset CFV WS "+ts(), "")
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	pickedAsset := map[string]any{"id": assetID, "title": "Component A", "asset_tag": ""}

	t.Run("create_persists_asset_custom_field_object", func(t *testing.T) {
		body := map[string]any{
			"title":               "Create with component " + ts(),
			"workspace_id":        workspaceID,
			"item_type_id":        itemTypeID,
			"custom_field_values": map[string]any{fieldKey: pickedAsset},
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var created map[string]any
		DecodeJSON(t, resp, &created)
		itemID := ExtractIDFromResponse(t, created)

		got := getItemCFV(t, server, itemID)
		value, ok := got[fieldKey].(map[string]any)
		if !ok {
			t.Fatalf("asset custom field %s missing after create, cfv = %v", fieldKey, got)
		}
		if idFor(value) != float64(assetID) {
			t.Fatalf("asset custom field id = %v, want %d (value = %v)", value["id"], assetID, value)
		}
	})

	t.Run("update_persists_asset_custom_field_object", func(t *testing.T) {
		itemID := createItemWithCFV(t, server, workspaceID, "Update component "+ts(), map[string]any{})

		patch := map[string]any{
			"custom_field_values": map[string]any{fieldKey: pickedAsset},
		}
		resp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/items/%d", itemID), patch)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		got := getItemCFV(t, server, itemID)
		value, ok := got[fieldKey].(map[string]any)
		if !ok {
			t.Fatalf("asset custom field %s missing after patch, cfv = %v", fieldKey, got)
		}
		if idFor(value) != float64(assetID) {
			t.Fatalf("asset custom field id = %v, want %d (value = %v)", value["id"], assetID, value)
		}
	})

	t.Run("create_persists_asset_custom_field_plain_id", func(t *testing.T) {
		body := map[string]any{
			"title":               "Create with component id " + ts(),
			"workspace_id":        workspaceID,
			"item_type_id":        itemTypeID,
			"custom_field_values": map[string]any{fieldKey: assetID},
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var created map[string]any
		DecodeJSON(t, resp, &created)
		itemID := ExtractIDFromResponse(t, created)

		got := getItemCFV(t, server, itemID)
		value, ok := got[fieldKey]
		if !ok || value == nil {
			t.Fatalf("asset custom field %s missing after create, cfv = %v", fieldKey, got)
		}
		if idFor(value) != float64(assetID) {
			t.Fatalf("asset custom field id = %v, want %d (value = %v)", value, assetID, value)
		}
	})
}

func createAssetForCFVTest(t *testing.T, server *TestServer, setID, typeID int, title string) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/asset-sets/%d/assets", setID), map[string]any{
		"title":         title,
		"asset_type_id": typeID,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var asset map[string]any
	DecodeJSON(t, resp, &asset)
	return ExtractIDFromResponse(t, asset)
}

func idFor(value any) float64 {
	switch v := value.(type) {
	case map[string]any:
		id, _ := v["id"].(float64)
		return id
	case float64:
		return v
	case json.Number:
		n, _ := v.Float64()
		return n
	}
	return -1
}
