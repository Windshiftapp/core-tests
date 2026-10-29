// create_item must accept custom_field_values so MCP and in-product chat can
// set custom fields at creation time instead of issuing a second update_item
// call. These tests pin the persisted result and the validation contract.
package tests

import (
	"strconv"
	"strings"
	"testing"
)

func TestMCP_CreateItemPersistsCustomFieldValues(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	w := SeedWorld(t, server)
	session := dialMCP(t, server)

	fieldID := CreateTestCustomField(t, server, "Create CF text "+ts(), "text", "")
	key := strconv.Itoa(fieldID)

	var created itemSummary
	callTool(t, session, "create_item", map[string]interface{}{
		"workspace_id":        w.Alpha.ID,
		"title":               "mcp-create-cfv sentinel",
		"custom_field_values": map[string]interface{}{key: "created with value"},
	}, &created)
	if created.ID == 0 {
		t.Fatalf("create_item returned no item: %+v", created)
	}

	cfv := getItemCFV(t, server, created.ID)
	if got := cfv[key]; got != "created with value" {
		t.Fatalf("custom_field_values[%s] = %v, want %q", key, got, "created with value")
	}
}

func TestMCP_CreateItemRejectsInvalidCustomFieldValue(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	w := SeedWorld(t, server)
	session := dialMCP(t, server)

	fieldID := CreateTestCustomField(t, server, "Create CF date "+ts(), "date", "")
	key := strconv.Itoa(fieldID)

	var rejected map[string]string
	callTool(t, session, "create_item", map[string]interface{}{
		"workspace_id":        w.Alpha.ID,
		"title":               "mcp-create-cfv-invalid",
		"custom_field_values": map[string]interface{}{key: "28.02.2026"},
	}, &rejected)
	if !strings.Contains(rejected["error"], "YYYY-MM-DD") {
		t.Fatalf("invalid date error = %q, want it to name the YYYY-MM-DD contract", rejected["error"])
	}

	// The rejected create must not leave an item behind.
	var listed struct {
		Items []itemSummary `json:"items"`
	}
	callTool(t, session, "list_items", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"limit":        200,
	}, &listed)
	for _, item := range listed.Items {
		if item.Title == "mcp-create-cfv-invalid" {
			t.Fatalf("invalid custom-field create was persisted: %+v", item)
		}
	}
}
