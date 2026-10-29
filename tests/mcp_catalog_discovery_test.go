// mcp_catalog_discovery_test covers the read-only workspace catalog tools
// list_item_types and list_statuses. These exist so an agent can discover the
// numeric item_type_id and status_id that create_item and transition_item
// require without hardcoding IDs or reading them out of the UI.
package tests

import (
	"strings"
	"testing"
)

func TestMCP_ListItemTypes(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	var out struct {
		ItemTypes []struct {
			ID             int    `json:"id"`
			Name           string `json:"name"`
			HierarchyLevel int    `json:"hierarchy_level"`
			IsDefault      bool   `json:"is_default"`
		} `json:"item_types"`
	}
	callTool(t, session, "list_item_types", map[string]interface{}{"workspace_id": w.Alpha.ID}, &out)

	byName := make(map[string]int, len(out.ItemTypes))
	for _, it := range out.ItemTypes {
		byName[it.Name] = it.ID
	}
	if byName["Task"] != w.defaultType {
		t.Fatalf("list_item_types Task id = %d, want %d (types=%+v)", byName["Task"], w.defaultType, out.ItemTypes)
	}
	if byName["Bug"] == 0 {
		t.Fatalf("list_item_types omitted the Bug type: %+v", out.ItemTypes)
	}
}

func TestMCP_ListStatuses(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	var out struct {
		Statuses []struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			CategoryName string `json:"category_name"`
			IsCompleted  bool   `json:"is_completed"`
		} `json:"statuses"`
	}
	callTool(t, session, "list_statuses", map[string]interface{}{"workspace_id": w.Alpha.ID}, &out)

	ids := make(map[string]int, len(out.Statuses))
	completed := make(map[string]bool, len(out.Statuses))
	categories := make(map[string]string, len(out.Statuses))
	for _, s := range out.Statuses {
		ids[s.Name] = s.ID
		completed[s.Name] = s.IsCompleted
		categories[s.Name] = s.CategoryName
	}

	if got := ids["Open"]; got != w.Statuses.Open {
		t.Fatalf("Open status id = %d, want %d (statuses=%+v)", got, w.Statuses.Open, out.Statuses)
	}
	if got := ids["In Progress"]; got != w.Statuses.InProgress {
		t.Fatalf("In Progress status id = %d, want %d", got, w.Statuses.InProgress)
	}
	if got := ids["Done"]; got != w.Statuses.Done {
		t.Fatalf("Done status id = %d, want %d", got, w.Statuses.Done)
	}
	if !completed["Done"] {
		t.Fatalf("Done status must report is_completed=true: %+v", out.Statuses)
	}
	if completed["Open"] {
		t.Fatalf("Open status must not report is_completed=true: %+v", out.Statuses)
	}
	if categories["Open"] == "" || categories["In Progress"] == "" || categories["Done"] == "" {
		t.Fatalf("statuses must carry category_name: %+v", out.Statuses)
	}
}

func TestMCP_CatalogDiscoveryAuthorization(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Catalog Discovery", shortKey("CATD"))
	LockDownWorkspace(t, server, workspaceID)

	t.Run("missing workspaces:read scope is denied at the token gate", func(t *testing.T) {
		token := createTokenWithScopesAsUser(t, server, "admin", "testpass123", []string{"mcp:access"})
		session := dialMCPWithToken(t, server, token)
		for _, tool := range []string{"list_statuses", "list_item_types"} {
			_, err := callMCPForContract(session, tool, map[string]interface{}{"workspace_id": workspaceID})
			if err == nil {
				t.Fatalf("%s: expected a token-scope denial", tool)
			}
			msg := err.Error()
			if !strings.Contains(msg, "workspaces:read") && !strings.Contains(msg, "403") && !strings.Contains(msg, "Forbidden") && !strings.Contains(msg, "scope") {
				t.Fatalf("%s unexpected scope error: %v", tool, err)
			}
		}
	})

	t.Run("inaccessible workspace is existence-masked", func(t *testing.T) {
		_, outsiderUsername, outsiderPassword := CreateTestUserWithCredentials(t, server,
			"catalog_outsider", "catalog_outsider@test.com")
		token := createTokenWithScopesAsUser(t, server, outsiderUsername, outsiderPassword,
			[]string{"mcp:access", "workspaces:read"})
		session := dialMCPWithToken(t, server, token)
		for _, tool := range []string{"list_statuses", "list_item_types"} {
			body, err := callMCPForContract(session, tool, map[string]interface{}{"workspace_id": workspaceID})
			if err != nil {
				t.Fatalf("%s: %v", tool, err)
			}
			if !strings.Contains(body, "workspace not found") {
				t.Fatalf("%s cross-workspace denial = %q, want workspace not found", tool, body)
			}
		}
	})
}
