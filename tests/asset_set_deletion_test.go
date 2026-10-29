package tests

import (
	"fmt"
	"net/http"
	"testing"
	"windshift/internal/itemevents"
)

func TestAssetSetDeletionRemovesLinksThroughApplication(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	setID, typeID := seedAssetSetAndType(t, server, "delete-linked-set")
	otherSet, otherType := seedAssetSetAndType(t, server, "preserved-linked-set")
	createAsset := func(set, typ int) int {
		response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/asset-sets/%d/assets", set), map[string]any{"title": "Linked asset", "asset_type_id": typ})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var data map[string]any
		DecodeJSON(t, response, &data)
		return ExtractIDFromResponse(t, data)
	}
	assetID := createAsset(setID, typeID)
	otherAsset := createAsset(otherSet, otherType)
	workspaceID, _ := CreateTestWorkspace(t, server, "Asset deletion", "ASDEL")
	itemID := CreateTestItem(t, server, workspaceID, "Linked item")
	for _, link := range []map[string]any{
		{"source_type": "asset", "source_id": assetID, "target_type": "item", "target_id": itemID},
		{"source_type": "asset", "source_id": otherAsset, "target_type": "asset", "target_id": assetID},
		{"source_type": "item", "source_id": itemID, "target_type": "asset", "target_id": otherAsset},
	} {
		link["link_type_id"] = 4
		response := MakeAuthRequest(t, server, http.MethodPost, "/links", link)
		AssertStatusCode(t, response, http.StatusCreated)
		response.Body.Close()
	}
	response := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/asset-sets/%d", setID), nil)
	AssertStatusCode(t, response, http.StatusNoContent)
	response.Body.Close()
	var dangling, preserved, removedEvents int
	if err := server.DB().QueryRow("SELECT COUNT(*) FROM item_links WHERE (source_type = 'asset' AND source_id = ?) OR (target_type = 'asset' AND target_id = ?)", assetID, assetID).Scan(&dangling); err != nil {
		t.Fatal(err)
	}
	if dangling != 0 {
		t.Fatalf("dangling links = %d, want 0", dangling)
	}
	if err := server.DB().QueryRow("SELECT COUNT(*) FROM item_links WHERE source_type = 'item' AND source_id = ? AND target_type = 'asset' AND target_id = ?", itemID, otherAsset).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != 1 {
		t.Fatalf("preserved links = %d, want 1", preserved)
	}
	if err := server.DB().QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ? AND aggregate_id = ?", itemevents.Unlinked, itemID).Scan(&removedEvents); err != nil {
		t.Fatal(err)
	}
	if removedEvents != 1 {
		t.Fatalf("item link removal events = %d, want 1", removedEvents)
	}
	surviving := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/assets/%d", otherAsset), nil)
	AssertStatusCode(t, surviving, http.StatusOK)
	surviving.Body.Close()
}
