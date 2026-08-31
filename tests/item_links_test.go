package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestItemLinkCRUD tests the complete lifecycle of creating, reading, and deleting
// links between work items.
func TestItemLinkCRUD(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	// Create workspace and items
	wsID, _ := CreateTestWorkspace(t, server, "Link CRUD Workspace", "")
	itemA := CreateTestItem(t, server, wsID, "Item A")
	itemB := CreateTestItem(t, server, wsID, "Item B")

	var linkID int

	t.Run("CreateLink", func(t *testing.T) {
		linkData := map[string]interface{}{
			"link_type_id": 2, // "Implements"
			"source_type":  "item",
			"source_id":    itemA,
			"target_type":  "item",
			"target_id":    itemB,
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusCreated)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)

		linkID = ExtractIDFromResponse(t, result)
		if linkID == 0 {
			t.Fatal("Expected non-zero link ID")
		}

		// Verify link fields
		if st, _ := result["source_type"].(string); st != "item" {
			t.Errorf("Expected source_type 'item', got %q", st)
		}
		if int(result["source_id"].(float64)) != itemA {
			t.Errorf("Expected source_id %d, got %v", itemA, result["source_id"])
		}
		if int(result["target_id"].(float64)) != itemB {
			t.Errorf("Expected target_id %d, got %v", itemB, result["target_id"])
		}
	})

	t.Run("GetLinksForSourceItem", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", itemA), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
			Incoming []map[string]interface{} `json:"incoming"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Outgoing) != 1 {
			t.Fatalf("Expected 1 outgoing link, got %d", len(result.Outgoing))
		}
		if int(result.Outgoing[0]["target_id"].(float64)) != itemB {
			t.Errorf("Expected outgoing link target_id %d, got %v", itemB, result.Outgoing[0]["target_id"])
		}
	})

	t.Run("GetLinksForTargetItem", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", itemB), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
			Incoming []map[string]interface{} `json:"incoming"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Incoming) != 1 {
			t.Fatalf("Expected 1 incoming link, got %d", len(result.Incoming))
		}
		if int(result.Incoming[0]["source_id"].(float64)) != itemA {
			t.Errorf("Expected incoming link source_id %d, got %v", itemA, result.Incoming[0]["source_id"])
		}
	})

	t.Run("DeleteLink", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/links/%d", linkID), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusNoContent)
	})

	t.Run("LinksEmptyAfterDelete", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", itemA), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
			Incoming []map[string]interface{} `json:"incoming"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Outgoing) != 0 {
			t.Errorf("Expected 0 outgoing links after delete, got %d", len(result.Outgoing))
		}
	})
}

// TestItemLinkDuplicatePrevention tests that duplicate links are rejected.
func TestItemLinkDuplicatePrevention(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Duplicate Link WS", "")
	itemA := CreateTestItem(t, server, wsID, "Dup Item A")
	itemB := CreateTestItem(t, server, wsID, "Dup Item B")

	linkData := map[string]interface{}{
		"link_type_id": 3, // "Depends On"
		"source_type":  "item",
		"source_id":    itemA,
		"target_type":  "item",
		"target_id":    itemB,
	}

	// Create first link
	resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
	resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	t.Run("ExactDuplicate", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusConflict)
	})

	t.Run("ReverseDuplicate", func(t *testing.T) {
		reverseData := map[string]interface{}{
			"link_type_id": 3,
			"source_type":  "item",
			"source_id":    itemB,
			"target_type":  "item",
			"target_id":    itemA,
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", reverseData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusConflict)
	})
}

// TestItemLinkSelfLinkPrevention tests that items cannot be linked to themselves.
func TestItemLinkSelfLinkPrevention(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Self Link WS", "")
	item := CreateTestItem(t, server, wsID, "Self Link Item")

	linkData := map[string]interface{}{
		"link_type_id": 4, // "Relates To"
		"source_type":  "item",
		"source_id":    item,
		"target_type":  "item",
		"target_id":    item,
	}

	resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusBadRequest)
}

// TestItemLinkValidation tests validation of required fields and invalid types.
func TestItemLinkValidation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Validation WS", "")
	item := CreateTestItem(t, server, wsID, "Validation Item")

	t.Run("MissingFields", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]interface{}{
			"source_type": "item",
			"source_id":   item,
		})
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("InvalidSourceType", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]interface{}{
			"link_type_id": 2,
			"source_type":  "invalid_type",
			"source_id":    item,
			"target_type":  "item",
			"target_id":    item + 1,
		})
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("InvalidTargetType", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]interface{}{
			"link_type_id": 2,
			"source_type":  "item",
			"source_id":    item,
			"target_type":  "bogus",
			"target_id":    item + 1,
		})
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusBadRequest)
	})
}

// TestItemLinkOnlyOneLinkPerPair tests that only one link can exist between
// a pair of items, regardless of the link type used.
func TestItemLinkOnlyOneLinkPerPair(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Multi Link WS", "")
	itemA := CreateTestItem(t, server, wsID, "Multi A")
	itemB := CreateTestItem(t, server, wsID, "Multi B")

	// First link should succeed
	t.Run("FirstLinkSucceeds", func(t *testing.T) {
		linkData := map[string]interface{}{
			"link_type_id": 2, // "Implements"
			"source_type":  "item",
			"source_id":    itemA,
			"target_type":  "item",
			"target_id":    itemB,
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusCreated)
	})

	// Second link with different type should be rejected (same pair)
	t.Run("SecondLinkSamePairConflicts", func(t *testing.T) {
		linkData := map[string]interface{}{
			"link_type_id": 3, // "Depends On"
			"source_type":  "item",
			"source_id":    itemA,
			"target_type":  "item",
			"target_id":    itemB,
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusConflict)
	})

	// Different pair should still work
	t.Run("DifferentPairSucceeds", func(t *testing.T) {
		itemC := CreateTestItem(t, server, wsID, "Multi C")

		linkData := map[string]interface{}{
			"link_type_id": 3, // "Depends On"
			"source_type":  "item",
			"source_id":    itemA,
			"target_type":  "item",
			"target_id":    itemC,
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusCreated)
	})
}

// TestItemLinkCrossWorkspace tests linking items across different workspaces.
func TestItemLinkCrossWorkspace(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsA, _ := CreateTestWorkspace(t, server, "Cross WS A", "")
	wsB, _ := CreateTestWorkspace(t, server, "Cross WS B", "")
	itemA := CreateTestItem(t, server, wsA, "Cross Item A")
	itemB := CreateTestItem(t, server, wsB, "Cross Item B")

	t.Run("CreateCrossWorkspaceLink", func(t *testing.T) {
		linkData := map[string]interface{}{
			"link_type_id": 4, // "Relates To"
			"source_type":  "item",
			"source_id":    itemA,
			"target_type":  "item",
			"target_id":    itemB,
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusCreated)
	})

	t.Run("VisibleFromSourceItem", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", itemA), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Outgoing) != 1 {
			t.Fatalf("Expected 1 outgoing link, got %d", len(result.Outgoing))
		}
		if int(result.Outgoing[0]["target_id"].(float64)) != itemB {
			t.Errorf("Expected target_id %d, got %v", itemB, result.Outgoing[0]["target_id"])
		}
	})

	t.Run("VisibleFromTargetItem", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", itemB), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Incoming []map[string]interface{} `json:"incoming"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Incoming) != 1 {
			t.Fatalf("Expected 1 incoming link, got %d", len(result.Incoming))
		}
	})
}

// TestItemLinkDeleteNonexistent tests deleting a link that doesn't exist.
func TestItemLinkDeleteNonexistent(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	resp := MakeAuthRequest(t, server, http.MethodDelete, "/links/999999", nil)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusNotFound)
}

// TestSearchLinkableItems tests the search endpoint for finding linkable items.
func TestSearchLinkableItems(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Search Link WS", "")
	CreateTestItem(t, server, wsID, "Alpha Feature")
	CreateTestItem(t, server, wsID, "Beta Feature")
	CreateTestItem(t, server, wsID, "Gamma Bugfix")

	t.Run("SearchByQuery", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/links/search?q=Feature", nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var items []map[string]interface{}
		DecodeJSON(t, resp, &items)

		if len(items) < 2 {
			t.Errorf("Expected at least 2 items matching 'Feature', got %d", len(items))
		}
	})

	t.Run("SearchFilterByType", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/links/search?q=&type=item", nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var items []map[string]interface{}
		DecodeJSON(t, resp, &items)

		for _, item := range items {
			if item["type"] != "item" {
				t.Errorf("Expected type 'item', got %v", item["type"])
			}
		}
	})

	t.Run("SearchWithLimit", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/links/search?q=&type=item&limit=1", nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var items []map[string]interface{}
		DecodeJSON(t, resp, &items)

		if len(items) > 1 {
			t.Errorf("Expected at most 1 item with limit=1, got %d", len(items))
		}
	})
}

// TestLinkTypesCRUD tests creating, reading, updating, and deleting custom link types.
func TestLinkTypesCRUD(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	t.Run("ListDefaultLinkTypes", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/link-types", nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var linkTypes []map[string]interface{}
		DecodeJSON(t, resp, &linkTypes)

		// 7 default link types are seeded
		if len(linkTypes) < 7 {
			t.Errorf("Expected at least 7 default link types, got %d", len(linkTypes))
		}

		// Verify "Tests" is first (ID=1) and system-protected
		found := false
		for _, lt := range linkTypes {
			if lt["name"] == "Tests" {
				found = true
				if !lt["is_system"].(bool) {
					t.Error("Expected 'Tests' to be a system link type")
				}
				break
			}
		}
		if !found {
			t.Error("Expected 'Tests' link type in defaults")
		}
	})

	var customLinkTypeID int

	t.Run("CreateCustomLinkType", func(t *testing.T) {
		data := map[string]interface{}{
			"name":          "Caused By",
			"description":   "Item was caused by another",
			"forward_label": "caused by",
			"reverse_label": "causes",
			"color":         "#dc2626",
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/link-types", data)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusCreated)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)

		customLinkTypeID = ExtractIDFromResponse(t, result)
		AssertJSONField(t, result, "name", "Caused By")
		AssertJSONField(t, result, "forward_label", "caused by")
		AssertJSONField(t, result, "reverse_label", "causes")
	})

	t.Run("GetCustomLinkType", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/link-types/%d", customLinkTypeID), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)

		AssertJSONField(t, result, "name", "Caused By")
	})

	t.Run("UpdateCustomLinkType", func(t *testing.T) {
		data := map[string]interface{}{
			"name":          "Root Cause",
			"description":   "Root cause relationship",
			"forward_label": "root cause of",
			"reverse_label": "caused by",
			"color":         "#b91c1c",
			"active":        true,
		}

		resp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/admin/link-types/%d", customLinkTypeID), data)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)

		AssertJSONField(t, result, "name", "Root Cause")
		AssertJSONField(t, result, "forward_label", "root cause of")
	})

	t.Run("UseCustomLinkType", func(t *testing.T) {
		wsID, _ := CreateTestWorkspace(t, server, "Custom LT WS", "")
		itemA := CreateTestItem(t, server, wsID, "Custom LT Item A")
		itemB := CreateTestItem(t, server, wsID, "Custom LT Item B")

		linkData := map[string]interface{}{
			"link_type_id": customLinkTypeID,
			"source_type":  "item",
			"source_id":    itemA,
			"target_type":  "item",
			"target_id":    itemB,
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusCreated)
	})

	t.Run("DeleteCustomLinkType", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/link-types/%d", customLinkTypeID), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusNoContent)
	})
}

// TestItemLinkResponseDetails verifies that link responses include joined metadata
// (link type name, source/target titles, workspace keys, etc.)
func TestItemLinkResponseDetails(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Details WS", "")
	itemA := CreateTestItem(t, server, wsID, "Detail Source")
	itemB := CreateTestItem(t, server, wsID, "Detail Target")

	// Create a link
	linkData := map[string]interface{}{
		"link_type_id": 3, // "Depends On"
		"source_type":  "item",
		"source_id":    itemA,
		"target_type":  "item",
		"target_id":    itemB,
	}

	resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
	resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	// Get links and verify rich details
	resp = MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", itemA), nil)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusOK)

	var result struct {
		Outgoing []map[string]interface{} `json:"outgoing"`
	}
	DecodeJSON(t, resp, &result)

	if len(result.Outgoing) != 1 {
		t.Fatalf("Expected 1 outgoing link, got %d", len(result.Outgoing))
	}

	link := result.Outgoing[0]

	// Verify joined metadata fields are present
	if link["link_type_name"] == nil || link["link_type_name"] == "" {
		t.Error("Expected link_type_name to be populated")
	}
	if link["source_title"] == nil || link["source_title"] == "" {
		t.Error("Expected source_title to be populated")
	}
	if link["target_title"] == nil || link["target_title"] == "" {
		t.Error("Expected target_title to be populated")
	}
	if link["created_by_name"] == nil || link["created_by_name"] == "" {
		t.Error("Expected created_by_name to be populated")
	}

	// Verify forward/reverse labels
	if link["link_type_forward_label"] == nil || link["link_type_forward_label"] == "" {
		t.Error("Expected link_type_forward_label to be populated")
	}
	if link["link_type_reverse_label"] == nil || link["link_type_reverse_label"] == "" {
		t.Error("Expected link_type_reverse_label to be populated")
	}
}

// TestItemLinkBulkOperations tests creating and deleting multiple links
// and verifies counts are accurate.
func TestItemLinkBulkOperations(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Bulk WS", "")
	hub := CreateTestItem(t, server, wsID, "Hub Item")

	const numSpokes = 5
	spokeIDs := make([]int, numSpokes)
	linkIDs := make([]int, numSpokes)

	for i := 0; i < numSpokes; i++ {
		spokeIDs[i] = CreateTestItem(t, server, wsID, fmt.Sprintf("Spoke %d", i))
	}

	t.Run("CreateMultipleLinks", func(t *testing.T) {
		for i, spokeID := range spokeIDs {
			linkData := map[string]interface{}{
				"link_type_id": 4, // "Relates To"
				"source_type":  "item",
				"source_id":    hub,
				"target_type":  "item",
				"target_id":    spokeID,
			}

			resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
			AssertStatusCode(t, resp, http.StatusCreated)

			var result map[string]interface{}
			DecodeJSON(t, resp, &result)

			linkIDs[i] = ExtractIDFromResponse(t, result)
			resp.Body.Close()
		}
	})

	t.Run("VerifyAllLinksPresent", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", hub), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Outgoing) != numSpokes {
			t.Errorf("Expected %d outgoing links, got %d", numSpokes, len(result.Outgoing))
		}
	})

	t.Run("DeleteSomeLinks", func(t *testing.T) {
		// Delete the first 2 links
		for i := 0; i < 2; i++ {
			resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/links/%d", linkIDs[i]), nil)
			resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusNoContent)
		}

		// Verify remaining count
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", hub), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Outgoing) != numSpokes-2 {
			t.Errorf("Expected %d outgoing links after deleting 2, got %d", numSpokes-2, len(result.Outgoing))
		}
	})
}

// TestItemLinkDirectionality tests that link direction (forward/reverse labels)
// is correctly reflected when querying from source vs target.
func TestItemLinkDirectionality(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Direction WS", "")
	parent := CreateTestItem(t, server, wsID, "Parent Feature")
	child := CreateTestItem(t, server, wsID, "Child Task")

	// Create "Depends On" link: child depends on parent
	linkData := map[string]interface{}{
		"link_type_id": 3, // "Depends On" (forward: "depends on", reverse: "blocks")
		"source_type":  "item",
		"source_id":    child,
		"target_type":  "item",
		"target_id":    parent,
	}

	resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
	resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	t.Run("SourceSeesOutgoingLink", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", child), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
			Incoming []map[string]interface{} `json:"incoming"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Outgoing) != 1 {
			t.Fatalf("Expected 1 outgoing link from child, got %d", len(result.Outgoing))
		}
		if len(result.Incoming) != 0 {
			t.Errorf("Expected 0 incoming links on child, got %d", len(result.Incoming))
		}

		// The outgoing link should show link_type_forward_label "depends on"
		if fl, _ := result.Outgoing[0]["link_type_forward_label"].(string); fl != "depends on" {
			t.Errorf("Expected link_type_forward_label 'depends on', got %q", fl)
		}
	})

	t.Run("TargetSeesIncomingLink", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", parent), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
			Incoming []map[string]interface{} `json:"incoming"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Incoming) != 1 {
			t.Fatalf("Expected 1 incoming link on parent, got %d", len(result.Incoming))
		}
		if len(result.Outgoing) != 0 {
			t.Errorf("Expected 0 outgoing links on parent, got %d", len(result.Outgoing))
		}

		// The incoming link should show link_type_reverse_label "blocks"
		if rl, _ := result.Incoming[0]["link_type_reverse_label"].(string); rl != "blocks" {
			t.Errorf("Expected link_type_reverse_label 'blocks', got %q", rl)
		}
	})
}

// TestItemLinkWithTestCase tests linking work items to test cases using the "Tests" link type.
func TestItemLinkWithTestCase(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "TC Link WS", "")
	item := CreateTestItem(t, server, wsID, "Feature to Test")

	// Create a test case
	tcData := map[string]interface{}{
		"title":  "Test Case for Feature",
		"name":   "TC-LINK-001",
		"status": "active",
	}
	tcResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/test-cases", wsID), tcData)
	defer tcResp.Body.Close()

	if tcResp.StatusCode != http.StatusOK && tcResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(tcResp.Body)
		t.Fatalf("Failed to create test case: %d - %s", tcResp.StatusCode, string(body))
	}

	var tc struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, tcResp, &tc)

	if tc.ID == 0 {
		t.Fatal("Failed to create test case")
	}

	t.Run("LinkItemToTestCase", func(t *testing.T) {
		linkData := map[string]interface{}{
			"link_type_id": 1, // "Tests"
			"source_type":  "item",
			"source_id":    item,
			"target_type":  "test_case",
			"target_id":    tc.ID,
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusCreated)
	})

	t.Run("LinkVisibleFromItem", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", item), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
		}
		DecodeJSON(t, resp, &result)

		if len(result.Outgoing) != 1 {
			t.Fatalf("Expected 1 outgoing link, got %d", len(result.Outgoing))
		}
		if result.Outgoing[0]["target_type"] != "test_case" {
			t.Errorf("Expected target_type 'test_case', got %v", result.Outgoing[0]["target_type"])
		}
	})

	t.Run("LinkVisibleFromTestCase", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/test-cases/%d/links", tc.ID), nil)
		defer resp.Body.Close()

		AssertStatusCode(t, resp, http.StatusOK)

		// Read body for inspection
		body, _ := io.ReadAll(resp.Body)
		var result struct {
			Outgoing []map[string]interface{} `json:"outgoing"`
			Incoming []map[string]interface{} `json:"incoming"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("Failed to decode: %v\nBody: %s", err, string(body))
		}

		// The test-cases links endpoint returns the link; verify it's present in either direction
		totalLinks := len(result.Outgoing) + len(result.Incoming)
		if totalLinks == 0 {
			t.Errorf("Expected at least 1 link from test case endpoint, got none\nBody: %s", string(body))
		}
	})
}

// TestItemLinkAllDefaultLinkTypes tests that all 7 default link types work for item-to-item linking
// (except "Tests" which requires test_case).
func TestItemLinkAllDefaultLinkTypes(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "All LT WS", "")

	// Default link types (from seed data):
	// 1: Tests (item <-> test_case only)
	// 2: Implements
	// 3: Depends On
	// 4: Relates To
	// 5: Links To
	// 6: Duplicates
	// 7: Child Of
	itemToItemTypes := []struct {
		id   int
		name string
	}{
		{2, "Implements"},
		{3, "Depends On"},
		{4, "Relates To"},
		{5, "Links To"},
		{6, "Duplicates"},
		{7, "Child Of"},
	}

	for _, lt := range itemToItemTypes {
		t.Run(lt.name, func(t *testing.T) {
			itemA := CreateTestItem(t, server, wsID, fmt.Sprintf("%s Source", lt.name))
			itemB := CreateTestItem(t, server, wsID, fmt.Sprintf("%s Target", lt.name))

			linkData := map[string]interface{}{
				"link_type_id": lt.id,
				"source_type":  "item",
				"source_id":    itemA,
				"target_type":  "item",
				"target_id":    itemB,
			}

			resp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkData)
			defer resp.Body.Close()

			AssertStatusCode(t, resp, http.StatusCreated)

			var result map[string]interface{}
			DecodeJSON(t, resp, &result)

			if int(result["link_type_id"].(float64)) != lt.id {
				t.Errorf("Expected link_type_id %d, got %v", lt.id, result["link_type_id"])
			}
		})
	}
}
